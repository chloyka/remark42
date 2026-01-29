package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jessevdk/go-flags"
	"go.uber.org/goleak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerApp(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	// send ping
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/ping", port))
	defer http.DefaultClient.CloseIdleConnections()
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "pong", string(body))

	// add comment
	client := http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/blah1", "site": "remark"}}`))
	require.NoError(t, err)
	req.SetBasicAuth("admin", "password")
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body, _ = io.ReadAll(resp.Body)
	t.Log(string(body))

	email, err := app.dataService.AdminStore.Email("")
	assert.NoError(t, err)
	assert.Equal(t, "admin@demo.remark42.com", email, "default admin email")

	cancel()
	app.Wait()
}

func TestServerApp_DevMode(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		o.AdminPasswd = "password"
		o.Auth.Dev = true
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	providers := app.restSrv.Authenticator.Providers()
	require.Equal(t, 11+1, len(providers), "extra auth provider")
	assert.Equal(t, "dev", providers[len(providers)-2].Name(), "dev auth provider")
	// send ping
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/ping", port))
	defer http.DefaultClient.CloseIdleConnections()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.NoError(t, resp.Body.Close())
	assert.Equal(t, "pong", string(body))

	cancel()
	app.Wait()
}

func TestServerApp_AnonMode(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		o.Auth.Anonymous = true
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	providers := app.restSrv.Authenticator.Providers()
	require.Equal(t, 11+1, len(providers), "extra auth provider for anon")
	assert.Equal(t, "anonymous", providers[len(providers)-1].Name(), "anon auth provider")

	client := http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()

	// send ping
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/api/v1/ping", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "pong", string(body))

	// try to login with good name
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=blah123&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// try to add a comment as good anonymous
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/blah1", "site": "remark"}}`))
	require.NoError(t, err)

	tkn, claims := getAuthFromCookie(t, app, resp)
	require.NotEmpty(t, tkn)
	assert.False(t, claims.User.BoolAttr("blocked"), "should not be blocked")
	req.Header.Add("X-JWT", tkn)
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// try to login with non-latin name
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=Раз_Два%20%20Три_34567&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// try to login with bad name
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=**blah123&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// try to login with short name
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=bl%%20%%20&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// try to login with name what have space in prefix
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=%%20somebody&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// try to login with name what have space in suffix
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=somebody%%20&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// try to login with long name
	time.Sleep(time.Second)
	ln := strings.Repeat("x", 65)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=%s&aud=remark", port, ln))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// try to login with admin name
	time.Sleep(time.Second)
	resp, err = client.Get(fmt.Sprintf("http://localhost:%d/auth/anonymous/login?user=umpUtun&aud=remark", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// try to add a comment as anonymous with admin name
	time.Sleep(time.Second)
	req, err = http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/blah1", "site": "remark"}}`))
	require.NoError(t, err)

	tkn, claims = getAuthFromCookie(t, app, resp)
	require.NotEmpty(t, tkn)
	assert.True(t, claims.User.BoolAttr("blocked"), "should be blocked")
	req.Header.Add("X-JWT", tkn)
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	cancel()
	app.Wait()
}

func getAuthFromCookie(t *testing.T, app *serverApp, resp *http.Response) (tkn string, claims token.Claims) {
	var err error
	for _, c := range resp.Cookies() {
		if c.Name == "JWT" {
			tkn = c.Value
			claims, err = app.restSrv.Authenticator.TokenService().Parse(c.Value)
			require.NoError(t, err)
		}
	}
	return tkn, claims
}

func TestServerApp_WithSSL(t *testing.T) {
	opts := ServerCommand{}
	sslPort := chooseRandomUnusedPort()
	opts.SetCommon(CommonOpts{RemarkURL: fmt.Sprintf("https://localhost:%d", sslPort), SharedSecret: "123456"})

	// prepare options
	p := flags.NewParser(&opts, flags.Default)
	port := chooseRandomUnusedPort()
	_, err := p.ParseArgs([]string{"--admin-passwd=password", "--port=" + strconv.Itoa(port), "--store.bolt.path=/tmp/xyz", "--backup=/tmp",
		"--avatar.type=bolt", "--avatar.bolt.file=/tmp/ava-test.db",
		"--ssl.type=static", "--ssl.cert=testdata/cert.pem", "--ssl.key=testdata/key.pem",
		"--ssl.port=" + strconv.Itoa(sslPort), "--image.fs.path=/tmp"})
	require.NoError(t, err)
	defer os.Remove("/tmp/xyz")
	defer os.Remove("/tmp/xyz/remark.db")
	defer os.Remove("/tmp/ava-test.db")

	// create app
	app, err := opts.newServerApp(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = app.run(ctx) }()
	waitForHTTPSServerStart(sslPort)

	client := http.Client{
		// prevent http redirect
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},

		// allow self-signed certificate
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	defer client.CloseIdleConnections()

	// check http to https redirect response
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/blah?param=1", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	assert.Equal(t, fmt.Sprintf("https://localhost:%d/blah?param=1", sslPort), resp.Header.Get("Location"))

	// check https server
	resp, err = client.Get(fmt.Sprintf("https://localhost:%d/ping", sslPort))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "pong", string(body))

	cancel()
	app.Wait()
}

func TestServerApp_WithRemote(t *testing.T) {
	opts := ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	// prepare options
	p := flags.NewParser(&opts, flags.Default)
	port := chooseRandomUnusedPort()
	_, err := p.ParseArgs([]string{"--admin-passwd=password", "--cache.type=none",
		"--store.type=rpc", "--store.rpc.api=http://127.0.0.1",
		"--port=" + strconv.Itoa(port), "--avatar.fs.path=/tmp",
		"--admin.type=rpc", "--admin.rpc.secret_per_site", "--admin.rpc.api=http://127.0.0.1"})
	require.NoError(t, err)
	opts.Auth.Github.CSEC, opts.Auth.Github.CID = "csec", "cid"
	opts.BackupLocation, opts.Image.FS.Path = "/tmp", "/tmp"

	// create app
	app, err := opts.newServerApp(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	// send ping
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/ping", port))
	defer http.DefaultClient.CloseIdleConnections()
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "pong", string(body))

	cancel()
	app.Wait()
}

func TestServerApp_Failed(t *testing.T) {
	opts := ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&opts, flags.Default)

	// RO bolt location
	_, err := p.ParseArgs([]string{"--backup=/tmp", "--store.bolt.path=/dev/null", "--image.fs.path=/tmp"})
	assert.NoError(t, err)
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err, "failed to make data store engine: failed to create bolt store: can't make directory /dev/null: mkdir /dev/null: not a directory")
	t.Log(err)

	// RO backup location
	opts = ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	_, err = p.ParseArgs([]string{"--store.bolt.path=/tmp", "--backup=/dev/null/not-writable"})
	assert.NoError(t, err)
	defer os.Remove("/tmp/remark.db")
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err, "failed to create backup store: can't make directory /dev/null/not-writable: mkdir /dev/null: not a directory")
	t.Log(err)

	// invalid url
	opts = ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "demo.remark42.com", SharedSecret: "123456"})

	_, err = p.ParseArgs([]string{"--backup=/tmp", "----store.bolt.path=/tmp"})
	assert.NoError(t, err)
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err, "invalid remark42 url demo.remark42.com")
	t.Log(err)

	// wrong store type
	opts = ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	_, err = p.ParseArgs([]string{"--backup=/tmp", "--store.type=blah"})
	assert.Error(t, err, "blah is invalid type")

	opts.Store.Type = "blah"
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err, "failed to make data store engine: unsupported store type blah")
	t.Log(err)

	// wrong redis location
	opts = ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})
	p = flags.NewParser(&opts, flags.Default)
	_, err = p.ParseArgs([]string{"--store.bolt.path=/tmp", "--cache.type=redis_pub_sub", "--cache.redis_addr=wrong_address"})
	assert.NoError(t, err)
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err,
		"failed to make cache: cache backend initialization, redis PubSub initialisation: "+
			"problem subscribing to channel remark42-cache on address wrong_address: "+
			"dial tcp: address wrong_address: missing port in address")
	t.Log(err)

	// wrong apple private key type
	opts = ServerCommand{}
	opts.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})
	p = flags.NewParser(&opts, flags.Default)
	_, err = p.ParseArgs([]string{"--auth.apple.cid=123", "--auth.apple.tid=123",
		"--auth.apple.kid=123", "--auth.apple.private-key-filepath=testdata/apple-bad.p8"})
	assert.NoError(t, err)
	_, err = opts.newServerApp(context.Background())
	assert.EqualError(t, err,
		"failed to make authenticator: an AppleProvider creating failed: "+
			"provided private key is not ECDSA")
	t.Log(err)
}

func TestServerApp_Shutdown(t *testing.T) {
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = chooseRandomUnusedPort()
		return o
	})
	time.AfterFunc(100*time.Millisecond, func() {
		cancel()
	})
	st := time.Now()
	err := app.run(ctx)
	assert.NoError(t, err)
	assert.True(t, time.Since(st).Seconds() < 1, "should take about 100msec")
	app.Wait()
}

func TestServerApp_MainSignal(t *testing.T) {
	done := make(chan struct{})
	go func() {
		<-done
		time.Sleep(250 * time.Millisecond)
		err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		require.NoError(t, err)
	}()

	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	port := chooseRandomUnusedPort()
	args := []string{"test", "--store.bolt.path=/tmp/xyz", "--backup=/tmp", "--avatar.type=bolt",
		"--avatar.bolt.file=/tmp/ava-test.db", "--port=" + strconv.Itoa(port), "--image.fs.path=/tmp"}
	defer os.Remove("/tmp/xyz")
	defer os.Remove("/tmp/xyz/remark.db")
	defer os.Remove("/tmp/ava-test.db")
	_, err := p.ParseArgs(args)
	require.NoError(t, err)
	st := time.Now()
	close(done)
	err = s.Execute(args)
	assert.NoError(t, err, "execute should be without errors")
	assert.True(t, time.Since(st).Seconds() < 5, "should take under five sec", time.Since(st).Seconds())
}

func TestServerApp_DeprecatedArgs(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	args := []string{
		"test",
		"--notify.type=email",
		"--notify.type=telegram",
		"--notify.users=none",
		"--notify.admins=none",
		"--img-proxy",
		"--notify.email.notify_admin",
		"--auth.email.host=smtp.example.org",
		"--auth.email.port=666",
		"--auth.email.tls",
		"--auth.email.user=test_user",
		"--auth.email.passwd=test_password",
		"--auth.email.timeout=15s",
		"--auth.email.template=file.tmpl",
		"--notify.telegram.token=abcd",
		"--notify.telegram.timeout=3m",
		"--notify.telegram.api=http://example.org",
		"--auth.twitter.cid=123",
		"--auth.twitter.csec=456",
	}
	assert.Empty(t, s.SMTP.Host)
	assert.Empty(t, s.SMTP.Port)
	assert.Empty(t, s.SMTP.TLS)
	assert.Empty(t, s.SMTP.Username)
	assert.Empty(t, s.SMTP.Password)
	assert.Empty(t, s.SMTP.TimeOut)
	_, err := p.ParseArgs(args)
	require.NoError(t, err)
	deprecatedFlags := s.HandleDeprecatedFlags()
	assert.ElementsMatch(t,
		[]DeprecatedFlag{
			{Old: "auth.email.host", New: "smtp.host", Version: "1.5"},
			{Old: "auth.email.port", New: "smtp.port", Version: "1.5"},
			{Old: "auth.email.tls", New: "smtp.tls", Version: "1.5"},
			{Old: "auth.email.user", New: "smtp.username", Version: "1.5"},
			{Old: "auth.email.passwd", New: "smtp.password", Version: "1.5"},
			{Old: "auth.email.timeout", New: "smtp.timeout", Version: "1.5"},
			{Old: "auth.email.template", Version: "1.5"},
			{Old: "img-proxy", New: "image-proxy.http2https", Version: "1.5"},
			{Old: "notify.email.notify_admin", New: "notify.admins=email", Version: "1.9"},
			{Old: "notify.type", New: "notify.(users|admins)", Version: "1.9"},
			{Old: "notify.telegram.token", New: "telegram.token", Version: "1.9"},
			{Old: "notify.telegram.timeout", New: "telegram.timeout", Version: "1.9"},
			{Old: "notify.telegram.api", Version: "1.9"},
			{Old: "auth.twitter.cid", Version: "1.14"},
			{Old: "auth.twitter.csec", Version: "1.14"},
		},
		deprecatedFlags)
	assert.Equal(t, "smtp.example.org", s.SMTP.Host)
	assert.Equal(t, 666, s.SMTP.Port)
	assert.Equal(t, true, s.SMTP.TLS)
	assert.Equal(t, "test_user", s.SMTP.Username)
	assert.Equal(t, "test_password", s.SMTP.Password)
	assert.Equal(t, 15*time.Second, s.SMTP.TimeOut)
}

func TestServerApp_DeprecatedArgsCollisions(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	args := []string{
		"test",
		"--auth.email.host=smtp-old.example.org",
		"--smtp.host=smtp-new.example.org",
		"--auth.email.port=666",
		"--smtp.port=999",
		"--auth.email.user=test_user",
		"--smtp.username=new_test_user",
		"--auth.email.passwd=test_password",
		"--smtp.password=new_test_password",
		"--auth.email.timeout=15s",
		"--smtp.timeout=20s",
		"--notify.type=telegram",
		"--notify.users=telegram",
		"--notify.admins=none",
		"--notify.telegram.token=abcd",
		"--telegram.token=dcba",
		"--notify.telegram.timeout=3m",
		"--telegram.timeout=5m",
	}
	_, err := p.ParseArgs(args)
	require.NoError(t, err)
	deprecatedFlagsCollisions := s.findDeprecatedFlagsCollisions()
	assert.ElementsMatch(t,
		[]DeprecatedFlag{
			{Old: "notify.type", New: "notify.(users|admins)", Collision: true},
			{Old: "auth.email.host", New: "smtp.host", Collision: true},
			{Old: "auth.email.port", New: "smtp.port", Collision: true},
			{Old: "auth.email.user", New: "smtp.username", Collision: true},
			{Old: "auth.email.passwd", New: "smtp.password", Collision: true},
			{Old: "auth.email.timeout", New: "smtp.timeout", Collision: true},
			{Old: "notify.telegram.token", New: "telegram.token", Collision: true},
			{Old: "notify.telegram.timeout", New: "telegram.timeout", Collision: true},
		},
		deprecatedFlagsCollisions)

	// case which should return nothing
	s = ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})
	p = flags.NewParser(&s, flags.Default)
	args = []string{
		"test",
		"--auth.email.host=smtp-old.example.org",
		"--smtp.host=''",
	}
	_, err = p.ParseArgs(args)
	require.NoError(t, err)
	deprecatedFlagsCollisions = s.findDeprecatedFlagsCollisions()
	assert.Empty(t, []DeprecatedFlag{}, deprecatedFlagsCollisions)
}

func Test_ACMEEmail(t *testing.T) {
	cmd := ServerCommand{}
	cmd.SetCommon(CommonOpts{RemarkURL: "https://remark.com:443", SharedSecret: "123456"})
	p := flags.NewParser(&cmd, flags.Default)
	args := []string{"--ssl.type=auto"}
	_, err := p.ParseArgs(args)
	require.NoError(t, err)
	cfg, err := cmd.makeSSLConfig()
	require.NoError(t, err)
	assert.Equal(t, "admin@remark.com", cfg.ACMEEmail)

	cmd = ServerCommand{}
	cmd.SetCommon(CommonOpts{RemarkURL: "https://remark.com", SharedSecret: "123456"})
	p = flags.NewParser(&cmd, flags.Default)
	args = []string{"--ssl.type=auto", "--ssl.acme-email=adminname@adminhost.com"}
	_, err = p.ParseArgs(args)
	require.NoError(t, err)
	cfg, err = cmd.makeSSLConfig()
	require.NoError(t, err)
	assert.Equal(t, "adminname@adminhost.com", cfg.ACMEEmail)

	cmd = ServerCommand{}
	cmd.SetCommon(CommonOpts{RemarkURL: "https://remark.com", SharedSecret: "123456"})
	p = flags.NewParser(&cmd, flags.Default)
	args = []string{"--ssl.type=auto", "--admin.type=shared", "--admin.shared.email=superadmin@admin.com"}
	_, err = p.ParseArgs(args)
	require.NoError(t, err)
	cfg, err = cmd.makeSSLConfig()
	require.NoError(t, err)
	assert.Equal(t, "superadmin@admin.com", cfg.ACMEEmail)

	cmd = ServerCommand{}
	cmd.SetCommon(CommonOpts{RemarkURL: "https://remark.com:443", SharedSecret: "123456"})
	p = flags.NewParser(&cmd, flags.Default)
	args = []string{"--ssl.type=auto", "--admin.type=shared"}
	_, err = p.ParseArgs(args)
	require.NoError(t, err)
	cfg, err = cmd.makeSSLConfig()
	require.NoError(t, err)
	assert.Equal(t, "admin@remark.com", cfg.ACMEEmail)
}

func TestServerAuthHooks(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	// make a token for user dev
	tkService := app.restSrv.Authenticator.TokenService()
	tkService.TokenDuration = time.Second

	claims := token.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"remark"},
			Issuer:    "remark",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Second)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
		},
		User: &token.User{
			ID:   "github_dev",
			Name: "developer one",
		},
	}
	tk, err := tkService.Token(claims)
	require.NoError(t, err)
	t.Log(tk)

	client := http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()

	// add comment
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/p/2018/12/29/podcast-630/", "site": "remark"}}`))
	require.NoError(t, err)
	req.Header.Set("X-JWT", tk)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusCreated, resp.StatusCode, "non-blocked user able to post")

	// try to add comment with no-aud claim
	badClaimsNoAud := claims
	badClaimsNoAud.Audience = jwt.ClaimStrings{""}
	tkNoAud, err := tkService.Token(badClaimsNoAud)
	require.NoError(t, err)
	t.Logf("no-aud claims: %s", tkNoAud)
	req, err = http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/p/2018/12/29/podcast-631/",
	"site": "remark"}}`))
	require.NoError(t, err)
	req.Header.Set("X-JWT", tkNoAud)
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "user without aud claim rejected, \n"+tkNoAud+"\n"+string(body))

	// try to add comment with multiple auds
	badClaimsMultipleAud := claims
	badClaimsMultipleAud.Audience = jwt.ClaimStrings{"remark", "second_aud"}
	tkMultipleAuds, err := tkService.Token(badClaimsMultipleAud)
	require.NoError(t, err)
	t.Logf("multiple aud claims: %s", tkMultipleAuds)
	req, err = http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/p/2018/12/29/podcast-631/",
	"site": "remark"}}`))
	require.NoError(t, err)
	req.Header.Set("X-JWT", tkMultipleAuds)
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "user with multiple auds claim rejected, \n"+tkMultipleAuds+"\n"+string(body))

	// try to add comment without user set
	badClaimsNoUser := claims
	badClaimsNoUser.Audience = jwt.ClaimStrings{"remark"}
	badClaimsNoUser.User = nil
	tkNoUser, err := tkService.Token(badClaimsNoUser)
	require.NoError(t, err)
	t.Logf("no user claims: %s", tkNoUser)
	req, err = http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123", "locator":{"url": "https://radio-t.com/p/2018/12/29/podcast-631/",
	"site": "remark"}}`))
	require.NoError(t, err)
	req.Header.Set("X-JWT", tkNoUser)
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "user without user information rejected, \n"+tkNoUser+"\n"+string(body))

	// block user github_dev as admin
	req, err = http.NewRequest(http.MethodPut,
		fmt.Sprintf("http://localhost:%d/api/v1/admin/user/github_dev?site=remark&block=1&ttl=10d", port), http.NoBody)
	assert.NoError(t, err)
	req.SetBasicAuth("admin", "password")
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "user github_dev blocked")
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	t.Log(string(b))

	// try add a comment with blocked user
	req, err = http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/api/v1/comment", port),
		strings.NewReader(`{"text": "test 123 blah", "locator":{"url": "https://radio-t.com/blah1", "site": "remark"}}`))
	require.NoError(t, err)
	req.Header.Set("X-JWT", tk)
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.True(t, resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized,
		"blocked user can't post, \n"+tk+"\n"+string(body))

	cancel()
	app.Wait()
	client.CloseIdleConnections()
}

func TestServerCommand_parseSameSite(t *testing.T) {
	tbl := []struct {
		inp string
		res http.SameSite
	}{
		{"", http.SameSiteDefaultMode},
		{"default", http.SameSiteDefaultMode},
		{"blah", http.SameSiteDefaultMode},
		{"none", http.SameSiteNoneMode},
		{"lax", http.SameSiteLaxMode},
		{"strict", http.SameSiteStrictMode},
	}

	cmd := ServerCommand{}
	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, tt.res, cmd.parseSameSite(tt.inp))
		})
	}
}

func Test_splitAtCommas(t *testing.T) {
	tbl := []struct {
		inp string
		res []string
	}{
		{"a string", []string{"a string"}},
		{"vv1, vv2, vv3", []string{"vv1", "vv2", "vv3"}},
		{`"vv1, blah", vv2, vv3`, []string{"vv1, blah", "vv2", "vv3"}},
		{
			`Access-Control-Allow-Headers:"DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type",header123:val, foo:"bar1,bar2"`,
			[]string{"Access-Control-Allow-Headers:\"DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type\"", "header123:val", "foo:\"bar1,bar2\""},
		},
		{"", []string{}},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, tt.res, splitAtCommas(tt.inp))
		})
	}
}

func Test_getAllowedDomains(t *testing.T) {
	tbl := []struct {
		s              ServerCommand
		allowedDomains []string
	}{
		// correct example, parsed and returned as allowed domain
		{ServerCommand{AllowedHosts: []string{}, CommonOpts: CommonOpts{RemarkURL: "https://remark42.example.org"}}, []string{"example.org"}},
		{ServerCommand{AllowedHosts: []string{}, CommonOpts: CommonOpts{RemarkURL: "http://remark42.example.org"}}, []string{"example.org"}},
		{ServerCommand{AllowedHosts: []string{}, CommonOpts: CommonOpts{RemarkURL: "http://localhost"}}, []string{"localhost"}},
		// incorrect URLs, so Hostname is empty but returned list doesn't include empty string as it would allow any domain
		{ServerCommand{AllowedHosts: []string{}, CommonOpts: CommonOpts{RemarkURL: "bad hostname"}}, []string{}},
		{ServerCommand{AllowedHosts: []string{}, CommonOpts: CommonOpts{RemarkURL: "not_a_hostname"}}, []string{}},
		// test removal of 'self', multiple AllowedHosts. No deduplication is expected
		{ServerCommand{AllowedHosts: []string{"'self'", "example.org", "test.example.org", "remark42.com"}, CommonOpts: CommonOpts{RemarkURL: "https://example.org"}}, []string{"example.org", "test.example.org", "remark42.com", "example.org"}},
	}
	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, tt.allowedDomains, tt.s.getAllowedDomains())
		})
	}
}

func TestJWTAuthConfig(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	args := []string{
		"test",
		"--auth.jwt.secret=my-secret-key",
		"--auth.jwt.algo=RS256",
		"--auth.jwt.header=Authorization",
		"--auth.jwt.issuer=my-issuer",
		"--auth.jwt.audience=my-audience",
		"--auth.jwt.map.id=user_id",
		"--auth.jwt.map.name=display_name",
		"--auth.jwt.map.email=user_email",
		"--auth.jwt.map.picture=avatar_url",
		"--auth.jwt.map.role=user_role",
	}
	_, err := p.ParseArgs(args)
	require.NoError(t, err)

	assert.Equal(t, "my-secret-key", s.Auth.JWT.Secret)
	assert.Equal(t, "RS256", s.Auth.JWT.Algo)
	assert.Equal(t, "Authorization", s.Auth.JWT.Header)
	assert.Equal(t, "my-issuer", s.Auth.JWT.Issuer)
	assert.Equal(t, "my-audience", s.Auth.JWT.Audience)
	assert.Equal(t, "user_id", s.Auth.JWT.Map.ID)
	assert.Equal(t, "display_name", s.Auth.JWT.Map.Name)
	assert.Equal(t, "user_email", s.Auth.JWT.Map.Email)
	assert.Equal(t, "avatar_url", s.Auth.JWT.Map.Picture)
	assert.Equal(t, "user_role", s.Auth.JWT.Map.Role)
}

func TestJWTAuthConfigDefaults(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	_, err := p.ParseArgs([]string{"test"})
	require.NoError(t, err)

	// verify defaults
	assert.Equal(t, "", s.Auth.JWT.Secret, "secret should be empty by default")
	assert.Equal(t, "HS256", s.Auth.JWT.Algo, "algo should default to HS256")
	assert.Equal(t, "X-Auth-Token", s.Auth.JWT.Header, "header should default to X-Auth-Token")
	assert.Equal(t, "", s.Auth.JWT.Issuer, "issuer should be empty by default")
	assert.Equal(t, "", s.Auth.JWT.Audience, "audience should be empty by default")
	assert.Equal(t, "sub", s.Auth.JWT.Map.ID, "map.id should default to sub")
	assert.Equal(t, "name", s.Auth.JWT.Map.Name, "map.name should default to name")
	assert.Equal(t, "email", s.Auth.JWT.Map.Email, "map.email should default to email")
	assert.Equal(t, "picture", s.Auth.JWT.Map.Picture, "map.picture should default to picture")
	assert.Equal(t, "role", s.Auth.JWT.Map.Role, "map.role should default to role")
}

func TestForwardAuthConfig(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	args := []string{
		"test",
		"--auth.forward.header=X-Forwarded-User",
		"--auth.forward.map.id=user_id",
		"--auth.forward.map.name=display_name",
		"--auth.forward.map.email=user_email",
		"--auth.forward.map.picture=avatar_url",
		"--auth.forward.map.role=user_role",
	}
	_, err := p.ParseArgs(args)
	require.NoError(t, err)

	assert.Equal(t, "X-Forwarded-User", s.Auth.Forward.Header)
	assert.Equal(t, "user_id", s.Auth.Forward.Map.ID)
	assert.Equal(t, "display_name", s.Auth.Forward.Map.Name)
	assert.Equal(t, "user_email", s.Auth.Forward.Map.Email)
	assert.Equal(t, "avatar_url", s.Auth.Forward.Map.Picture)
	assert.Equal(t, "user_role", s.Auth.Forward.Map.Role)
}

func TestForwardAuthConfigDefaults(t *testing.T) {
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	_, err := p.ParseArgs([]string{"test"})
	require.NoError(t, err)

	// verify defaults
	assert.Equal(t, "", s.Auth.Forward.Header, "header should be empty by default")
	assert.Equal(t, "sub", s.Auth.Forward.Map.ID, "map.id should default to sub")
	assert.Equal(t, "name", s.Auth.Forward.Map.Name, "map.name should default to name")
	assert.Equal(t, "email", s.Auth.Forward.Map.Email, "map.email should default to email")
	assert.Equal(t, "picture", s.Auth.Forward.Map.Picture, "map.picture should default to picture")
	assert.Equal(t, "role", s.Auth.Forward.Map.Role, "map.role should default to role")
}

func TestJWTAuthConfigEnvVars(t *testing.T) {
	// test that env vars are correctly mapped
	t.Setenv("AUTH_JWT_SECRET", "env-secret")
	t.Setenv("AUTH_JWT_ALGO", "ES256")
	t.Setenv("AUTH_JWT_HEADER", "X-Custom-Header")
	t.Setenv("AUTH_JWT_ISSUER", "env-issuer")
	t.Setenv("AUTH_JWT_AUDIENCE", "env-audience")
	t.Setenv("AUTH_JWT_MAP_ID", "env_id")
	t.Setenv("AUTH_JWT_MAP_NAME", "env_name")
	t.Setenv("AUTH_JWT_MAP_EMAIL", "env_email")
	t.Setenv("AUTH_JWT_MAP_PICTURE", "env_picture")
	t.Setenv("AUTH_JWT_MAP_ROLE", "env_role")

	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	_, err := p.ParseArgs([]string{"test"})
	require.NoError(t, err)

	assert.Equal(t, "env-secret", s.Auth.JWT.Secret)
	assert.Equal(t, "ES256", s.Auth.JWT.Algo)
	assert.Equal(t, "X-Custom-Header", s.Auth.JWT.Header)
	assert.Equal(t, "env-issuer", s.Auth.JWT.Issuer)
	assert.Equal(t, "env-audience", s.Auth.JWT.Audience)
	assert.Equal(t, "env_id", s.Auth.JWT.Map.ID)
	assert.Equal(t, "env_name", s.Auth.JWT.Map.Name)
	assert.Equal(t, "env_email", s.Auth.JWT.Map.Email)
	assert.Equal(t, "env_picture", s.Auth.JWT.Map.Picture)
	assert.Equal(t, "env_role", s.Auth.JWT.Map.Role)
}

func TestForwardAuthConfigEnvVars(t *testing.T) {
	// test that env vars are correctly mapped
	t.Setenv("AUTH_FORWARD_HEADER", "X-Forwarded-User")
	t.Setenv("AUTH_FORWARD_MAP_ID", "env_id")
	t.Setenv("AUTH_FORWARD_MAP_NAME", "env_name")
	t.Setenv("AUTH_FORWARD_MAP_EMAIL", "env_email")
	t.Setenv("AUTH_FORWARD_MAP_PICTURE", "env_picture")
	t.Setenv("AUTH_FORWARD_MAP_ROLE", "env_role")

	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})

	p := flags.NewParser(&s, flags.Default)
	_, err := p.ParseArgs([]string{"test"})
	require.NoError(t, err)

	assert.Equal(t, "X-Forwarded-User", s.Auth.Forward.Header)
	assert.Equal(t, "env_id", s.Auth.Forward.Map.ID)
	assert.Equal(t, "env_name", s.Auth.Forward.Map.Name)
	assert.Equal(t, "env_email", s.Auth.Forward.Map.Email)
	assert.Equal(t, "env_picture", s.Auth.Forward.Map.Picture)
	assert.Equal(t, "env_role", s.Auth.Forward.Map.Role)
}

func TestServerApp_JWTAuthConfigPassedToRest(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		o.Auth.JWT.Secret = "test-jwt-secret"
		o.Auth.JWT.Algo = "HS256"
		o.Auth.JWT.Header = "X-Custom-JWT"
		o.Auth.JWT.Issuer = "test-issuer"
		o.Auth.JWT.Audience = "test-audience"
		o.Auth.JWT.Map.ID = "uid"
		o.Auth.JWT.Map.Name = "uname"
		o.Auth.JWT.Map.Email = "uemail"
		o.Auth.JWT.Map.Picture = "upic"
		o.Auth.JWT.Map.Role = "urole"
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	assert.Equal(t, "test-jwt-secret", app.restSrv.JWTAuthConf.Secret)
	assert.Equal(t, "HS256", app.restSrv.JWTAuthConf.Algo)
	assert.Equal(t, "X-Custom-JWT", app.restSrv.JWTAuthConf.Header)
	assert.Equal(t, "test-issuer", app.restSrv.JWTAuthConf.Issuer)
	assert.Equal(t, "test-audience", app.restSrv.JWTAuthConf.Audience)
	assert.Equal(t, "uid", app.restSrv.JWTAuthConf.Map.ID)
	assert.Equal(t, "uname", app.restSrv.JWTAuthConf.Map.Name)
	assert.Equal(t, "uemail", app.restSrv.JWTAuthConf.Map.Email)
	assert.Equal(t, "upic", app.restSrv.JWTAuthConf.Map.Picture)
	assert.Equal(t, "urole", app.restSrv.JWTAuthConf.Map.Role)

	cancel()
	app.Wait()
}

func TestServerApp_ForwardAuthConfigPassedToRest(t *testing.T) {
	port := chooseRandomUnusedPort()
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = port
		o.Auth.Forward.Header = "X-Forwarded-User"
		o.Auth.Forward.Map.ID = "fid"
		o.Auth.Forward.Map.Name = "fname"
		o.Auth.Forward.Map.Email = "femail"
		o.Auth.Forward.Map.Picture = "fpic"
		o.Auth.Forward.Map.Role = "frole"
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(port)

	assert.Equal(t, "X-Forwarded-User", app.restSrv.ForwardAuthConf.Header)
	assert.Equal(t, "fid", app.restSrv.ForwardAuthConf.Map.ID)
	assert.Equal(t, "fname", app.restSrv.ForwardAuthConf.Map.Name)
	assert.Equal(t, "femail", app.restSrv.ForwardAuthConf.Map.Email)
	assert.Equal(t, "fpic", app.restSrv.ForwardAuthConf.Map.Picture)
	assert.Equal(t, "frole", app.restSrv.ForwardAuthConf.Map.Role)

	cancel()
	app.Wait()
}

func TestAddAuthProviders_CountsJWTAndForwardAuth(t *testing.T) {
	// Test that addAuthProviders counts JWT auth as a provider (no "no auth providers" warning)
	// We can't directly check providersCount, but we can verify the function doesn't error
	// with JWT secret set and no other providers.
	s := ServerCommand{}
	s.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "123456"})
	s.Auth.JWT.Secret = "test-secret"
	s.Auth.JWT.Algo = "HS256"
	s.Auth.JWT.Header = "X-Auth-Token"
	s.Auth.Forward.Header = "X-Forwarded-User"

	// Create a minimal authenticator to test addAuthProviders
	app, ctx, cancel := prepServerApp(t, func(o ServerCommand) ServerCommand {
		o.Port = chooseRandomUnusedPort()
		o.Auth.JWT.Secret = "test-secret"
		o.Auth.JWT.Algo = "HS256"
		o.Auth.JWT.Header = "X-Auth-Token"
		o.Auth.Forward.Header = "X-Forwarded-User"
		return o
	})

	go func() { _ = app.run(ctx) }()
	waitForHTTPServerStart(app.Port)

	// verify the server started without error (providers count included JWT and forward auth)
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/v1/ping", app.Port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "pong", string(body))

	cancel()
	app.Wait()
}

func chooseRandomUnusedPort() (port int) {
	for i := 0; i < 10; i++ {
		port = 40000 + int(rand.Int31n(10000))
		if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
			_ = ln.Close()
			break
		}
	}
	return port
}

func waitForHTTPServerStart(port int) {
	// wait for up to 3 seconds for server to start before returning it
	client := http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for i := 0; i < 300; i++ {
		time.Sleep(time.Millisecond * 10)
		if resp, err := client.Get(fmt.Sprintf("http://localhost:%d", port)); err == nil {
			_ = resp.Body.Close()
			return
		}
	}
}

func waitForHTTPSServerStart(port int) {
	// wait for up to 3 seconds for HTTPS server to start
	for i := 0; i < 300; i++ {
		time.Sleep(time.Millisecond * 10)
		conn, _ := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Millisecond*10)
		if conn != nil {
			_ = conn.Close()
			break
		}
	}
}

func prepServerApp(t *testing.T, fn func(o ServerCommand) ServerCommand) (*serverApp, context.Context, context.CancelFunc) {
	cmd := ServerCommand{}
	cmd.SetCommon(CommonOpts{RemarkURL: "https://demo.remark42.com", SharedSecret: "secret"})

	// prepare options
	p := flags.NewParser(&cmd, flags.Default)
	_, err := p.ParseArgs([]string{"--admin-passwd=password", "--site=remark"})
	require.NoError(t, err)
	cmd.Avatar.FS.Path, cmd.Avatar.Type, cmd.BackupLocation, cmd.Image.FS.Path = "/tmp/remark42_test", "fs", "/tmp/remark42_test", "/tmp/remark42_test"
	cmd.Store.Bolt.Timeout = 10 * time.Second
	cmd.Auth.Apple.CID, cmd.Auth.Apple.KID, cmd.Auth.Apple.TID = "cid", "kid", "tid"
	cmd.Auth.Apple.PrivateKeyFilePath = "testdata/apple.p8"
	cmd.Auth.Github.CSEC, cmd.Auth.Github.CID = "csec", "cid"
	cmd.Auth.Google.CSEC, cmd.Auth.Google.CID = "csec", "cid"
	cmd.Auth.Facebook.CSEC, cmd.Auth.Facebook.CID = "csec", "cid"
	cmd.Auth.Yandex.CSEC, cmd.Auth.Yandex.CID = "csec", "cid"
	cmd.Auth.Microsoft.CSEC, cmd.Auth.Microsoft.CID = "csec", "cid"
	cmd.Auth.Twitter.CSEC, cmd.Auth.Twitter.CID = "csec", "cid"
	cmd.Auth.Patreon.CSEC, cmd.Auth.Patreon.CID = "csec", "cid"
	cmd.Auth.Discord.CSEC, cmd.Auth.Discord.CID = "csec", "cid"
	cmd.Auth.Telegram = true
	cmd.Telegram.Token = "token"
	cmd.Auth.Email.Enable = true
	cmd.Auth.Email.MsgTemplate = "testdata/email.tmpl"
	cmd.BackupLocation = "/tmp"
	cmd.Notify.Users = []string{"email"}
	cmd.Notify.Admins = []string{"email"}
	cmd.Notify.Email.From = "from@example.org"
	cmd.Notify.Email.VerificationSubject = "test verification email subject"
	cmd.SMTP.Host = "127.0.0.1"
	cmd.SMTP.Port = 25
	cmd.SMTP.Username = "test_user"
	cmd.SMTP.Password = "test_password"
	cmd.SMTP.TimeOut = time.Second
	cmd.UpdateLimit = 10
	cmd.Admin.Type = "shared"
	cmd.Admin.Shared.Admins = []string{"id1", "id2"}
	cmd.RestrictedNames = []string{"umputun", "bobuk"}
	cmd.emailMsgTemplatePath = "../../templates/email_reply.html.tmpl"
	cmd.emailVerificationTemplatePath = "../../templates/email_confirmation_subscription.html.tmpl"

	cmd = fn(cmd)
	// as is uses port, call it after fn which could set it
	cmd.Store.Bolt.Path = fmt.Sprintf("/tmp/%d", cmd.Port)

	app, ctx, cancel := createAppFromCmd(t, cmd)

	// cleanup the remark.db file after context is canceled
	go func() {
		<-ctx.Done()
		os.RemoveAll(cmd.Store.Bolt.Path)
		os.RemoveAll(cmd.Avatar.FS.Path)

	}()

	return app, ctx, cancel
}

func createAppFromCmd(t *testing.T, cmd ServerCommand) (*serverApp, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	app, err := cmd.newServerApp(ctx)
	require.NoError(t, err)
	return app, ctx, cancel
}

func TestMain(m *testing.M) {
	// ignore is added only for GitHub Actions, can't reproduce locally
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*Server).Shutdown"),
		// this will be fixed in https://github.com/hashicorp/golang-lru/issues/159
		goleak.IgnoreTopFunction("github.com/hashicorp/golang-lru/v2/expirable.NewLRU[...].func1"),
	)
}
