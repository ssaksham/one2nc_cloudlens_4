package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

type signedRequest struct{ key, region, token string }

type gate struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	fail     bool

	startOnce, releaseOnce, finishOnce sync.Once
}

func newGate(fail bool) *gate {
	return &gate{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}), fail: fail}
}

func (g *gate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

func (g *gate) isStarted() bool {
	select {
	case <-g.started:
		return true
	default:
		return false
	}
}

// fixture is a local STS/EC2 endpoint. Role lookups can be held open per role
// (or role@region) so the harness controls exactly when credentials resolve.
type fixture struct {
	url string
	srv *http.Server

	mu       sync.Mutex
	requests []signedRequest
	gates    map[string]*gate
}

func newFixture(gates map[string]*gate) *fixture {
	if gates == nil {
		gates = map[string]*gate{}
	}
	f := &fixture{gates: gates}
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.handle)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	f.url = "http://" + ln.Addr().String()
	f.srv = &http.Server{Handler: mux}
	go f.srv.Serve(ln)
	return f
}

func (f *fixture) port() string {
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.url, "http://"))
	return port
}

func (f *fixture) close() { f.srv.Close() }

func parseSigned(r *http.Request) signedRequest {
	var req signedRequest
	if _, after, ok := strings.Cut(r.Header.Get("Authorization"), "Credential="); ok {
		fields := strings.Fields(after)
		if len(fields) > 0 {
			parts := strings.Split(strings.TrimSuffix(fields[0], ","), "/")
			if len(parts) >= 4 {
				req.key, req.region = parts[0], parts[2]
			}
		}
	}
	req.token = r.Header.Get("X-Amz-Security-Token")
	return req
}

const ec2Response = `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>local</requestId><reservationSet/></DescribeInstancesResponse>`

// Checking who a loaded profile is, is a reasonable way to validate it, so
// the fixture answers STS GetCallerIdentity as well.
const callerIdentityResponse = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>arn:aws:iam::123456789012:user/local</Arn><UserId>LOCALUSER</UserId><Account>123456789012</Account></GetCallerIdentityResult><ResponseMetadata><RequestId>local</RequestId></ResponseMetadata></GetCallerIdentityResponse>`

func roleResponse(key, token string) string {
	return `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>` + key + `</AccessKeyId><SecretAccessKey>rolesecret</SecretAccessKey><SessionToken>` + token + `</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials><AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/dev/session</Arn><AssumedRoleId>role:session</AssumedRoleId></AssumedRoleUser></AssumeRoleResult></AssumeRoleResponse>`
}

func (f *fixture) handle(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "text/xml")
	// Record every signed request, STS lookups included: their credential
	// scope shows which region a pending selection is being loaded for.
	seen := parseSigned(r)
	f.mu.Lock()
	f.requests = append(f.requests, seen)
	f.mu.Unlock()
	if r.Form.Get("Action") == "AssumeRole" {
		role := strings.TrimPrefix(r.Form.Get("RoleArn"), "arn:aws:iam::123456789012:role/")
		signed := parseSigned(r)
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=sourceKEY/") {
			http.Error(w, "incorrect source profile", http.StatusForbidden)
			return
		}
		g := f.gates[role+"@"+signed.region]
		if g == nil {
			g = f.gates[role]
		}
		if g == nil {
			fmt.Fprint(w, roleResponse("ROLEKEY", "roletoken"))
			return
		}
		g.startOnce.Do(func() { close(g.started) })
		defer g.finishOnce.Do(func() { close(g.finished) })
		select {
		case <-g.release:
		case <-r.Context().Done():
			return
		}
		if g.fail {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>cannot load role %s</Message></Error><RequestId>local</RequestId></ErrorResponse>`, role)
			return
		}
		fmt.Fprint(w, roleResponse(role+"KEY", role+"TOKEN"))
		return
	}
	if r.Form.Get("Action") == "GetCallerIdentity" {
		fmt.Fprint(w, callerIdentityResponse)
		return
	}
	fmt.Fprint(w, ec2Response)
}

func (f *fixture) mark() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fixture) hasSince(mark int, want signedRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, req := range f.requests[mark:] {
		if req == want {
			return true
		}
	}
	return false
}
