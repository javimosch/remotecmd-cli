package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// admissionRig is a relay with a secret and a way to dial it with or without the secret.
type admissionRig struct {
	t   *testing.T
	rs  *RelayServer
	srv *httptest.Server
	u   string
}

func newAdmissionRig(t *testing.T) *admissionRig {
	t.Helper()
	rs := NewRelayServer()
	rs.secret = "S"
	srv := httptest.NewServer(rs.mux())
	t.Cleanup(srv.Close)
	return &admissionRig{t: t, rs: rs, srv: srv, u: "ws" + strings.TrimPrefix(srv.URL, "http")}
}

func (a *admissionRig) dial(auth bool) (*websocket.Conn, error) {
	h := http.Header{}
	if auth {
		h.Set("Authorization", "Bearer S")
	}
	c, _, err := websocket.DefaultDialer.Dial(a.u, h)
	if err == nil {
		a.t.Cleanup(func() { c.Close() })
	}
	return c, err
}

func readMsg(c *websocket.Conn) Message {
	var m Message
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	c.ReadJSON(&m)
	return m
}

// register dials without the secret and sends a register message; it returns the answer ("registered", or an error reply / closed connection).
func (a *admissionRig) register(name, token, key string) Message {
	a.t.Helper()
	c, err := a.dial(false)
	if err != nil {
		return Message{Type: "dial-refused", Error: err.Error()}
	}
	c.WriteJSON(&Message{Type: "register", Name: name, Token: token, ActivationKey: key})
	return readMsg(c)
}

// The exempt list is a file the relay re-reads: a name added while the relay runs registers at once, and is refused again when it is removed (no restart).
func TestExemptListIsHotReloaded(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)

	if m := rig.register("laptop", "tok", ""); m.Type == "registered" {
		t.Fatal("nothing admits the node yet, but it registered")
	}
	if err := addExemptNode("laptop"); err != nil {
		t.Fatal(err)
	}
	if m := rig.register("laptop", "tok", ""); m.Type != "registered" {
		t.Fatalf("after add-exempt: %+v", m)
	}
	if err := removeExemptNode("laptop"); err != nil {
		t.Fatal(err)
	}
	if m := rig.register("laptop2", "tok", ""); m.Type == "registered" {
		t.Fatal("an unrelated name registered")
	}
	// the live relay (not a new one) refuses it again
	time.Sleep(20 * time.Millisecond)
	if m := rig.register("laptop", "tok2", ""); m.Type == "registered" {
		t.Fatalf("after remove-exempt the name registered: %+v", m)
	}
}

// An activation key admits a node that is neither exempt nor known; the relay then remembers the node, so it reconnects WITHOUT the key, with the same token only.
func TestActivationKeyAdmitsAndRemembersTheNode(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)
	if err := addActivationKey("K-123"); err != nil {
		t.Fatal(err)
	}

	if m := rig.register("mac", "tok", "wrong-key"); m.Type == "registered" {
		t.Fatal("a wrong key admitted the node")
	}
	if m := rig.register("mac", "tok", "K-123"); m.Type != "registered" {
		t.Fatalf("a valid key did not admit the node: %+v", m)
	}
	if m := rig.register("mac", "tok", ""); m.Type != "registered" {
		t.Fatalf("the known node could not reconnect without the key: %+v", m)
	}
	// someone else claiming the name with another token and no key: refused (the name alone proves nothing)
	if m := rig.register("mac", "other-token", ""); m.Type == "registered" {
		t.Fatalf("a different token took the known name: %+v", m)
	}
	// the key can be removed; the known node still reconnects, a NEW node with that key does not
	if err := removeActivationKey("K-123"); err != nil {
		t.Fatal(err)
	}
	if m := rig.register("mac", "tok", ""); m.Type != "registered" {
		t.Fatalf("a known node lost access when the key was removed: %+v", m)
	}
	if m := rig.register("other", "tok", "K-123"); m.Type == "registered" {
		t.Fatalf("a removed key admitted a new node: %+v", m)
	}
}

// forget-node revokes a node admitted with a key.
func TestForgetNodeRevokes(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)
	addActivationKey("K")
	if m := rig.register("mac", "tok", "K"); m.Type != "registered" {
		t.Fatalf("admit: %+v", m)
	}
	removeActivationKey("K")
	if ok, err := knownNodes.forget("mac"); !ok || err != nil {
		t.Fatalf("forget: %v %v", ok, err)
	}
	if m := rig.register("mac", "tok", ""); m.Type == "registered" {
		t.Fatalf("a forgotten node registered: %+v", m)
	}
	if ok, _ := knownNodes.forget("mac"); ok {
		t.Error("forgetting twice should report nothing to forget")
	}
}

// The token is kept as a digest, never in clear.
func TestKnownNodesStoreOnlyADigest(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	if err := knownNodes.remember("mac", "super-secret-token"); err != nil {
		t.Fatal(err)
	}
	data := readFileT(t, knownNodesPath())
	if strings.Contains(data, "super-secret-token") {
		t.Fatalf("the token is stored in clear: %s", data)
	}
	if !knownNodes.matches("mac", "super-secret-token") || knownNodes.matches("mac", "nope") {
		t.Error("matches wrong")
	}
}

// A node admitted this way is still only a daemon: it cannot execute commands, and unauthenticated clients are still refused.
func TestAdmittedNodeCannotActAsClient(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)
	addActivationKey("K")
	c, err := rig.dial(false)
	if err != nil {
		t.Fatal(err)
	}
	c.WriteJSON(&Message{Type: "register", Name: "mac", Token: "tok", ActivationKey: "K"})
	if m := readMsg(c); m.Type != "registered" {
		t.Fatalf("register: %+v", m)
	}
	c.WriteJSON(&Message{Type: "execute", ID: "1", Target: "mac", Token: "tok", Cmd: "id"})
	if m := readMsg(c); !strings.Contains(m.Error, "authentication required") {
		t.Errorf("execute from an admitted node's connection: %+v", m)
	}
	anon, _ := rig.dial(false)
	anon.WriteJSON(&Message{Type: "execute", ID: "2", Target: "mac", Token: "tok", Cmd: "id"})
	if m := readMsg(anon); !strings.Contains(m.Error, "authentication required") {
		t.Errorf("unauthenticated execute: %+v", m)
	}
}

// With nothing that could admit an unauthenticated connection, it is refused at the handshake, as before.
func TestNoAdmissionPathRefusesAtHandshake(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)
	if _, err := rig.dial(false); err == nil {
		t.Fatal("an unauthenticated connection was let in although nothing can admit it")
	}
	if _, err := rig.dial(true); err != nil {
		t.Fatalf("an authenticated connection was refused: %v", err)
	}
	addActivationKey("K")
	if _, err := rig.dial(false); err != nil {
		t.Fatalf("with an activation key configured the handshake should pass (the register decides): %v", err)
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The whole pairing on a relay that has a secret: the listener (with the secret) waits on a code; the new node (without it) registers with the activation key, sends
// its pair message, the listener gets the token; later the node reconnects without any key and an authenticated client can run a command on it.
func TestPairingOnARelayWithASecret(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	rig := newAdmissionRig(t)
	addActivationKey("K")

	listener, err := rig.dial(true)
	if err != nil {
		t.Fatal(err)
	}
	listener.WriteJSON(&Message{Type: "pair_listen", Code: "c0de", RequireActivationKey: true})
	time.Sleep(50 * time.Millisecond)

	node, err := rig.dial(false)
	if err != nil {
		t.Fatal(err)
	}
	node.WriteJSON(&Message{Type: "register", Name: "mac.local", Token: "node-token", ActivationKey: "K"})
	if m := readMsg(node); m.Type != "registered" {
		t.Fatalf("register: %+v", m)
	}
	node.WriteJSON(&Message{Type: "pair", Code: "c0de", Token: "node-token", Hostname: "mac.local", ActivationKey: "K"})
	if m := readMsg(listener); m.Type != "pair_done" || m.Token != "node-token" || m.Hostname != "mac.local" {
		t.Fatalf("listener: %+v", m)
	}
	if m := readMsg(node); m.Type != "pair_confirmed" {
		t.Fatalf("node: %+v", m)
	}
	node.Close()
	time.Sleep(50 * time.Millisecond)

	// it comes back later without the key (the daemon deleted it after the confirmation)
	node2, err := rig.dial(false)
	if err != nil {
		t.Fatal(err)
	}
	node2.WriteJSON(&Message{Type: "register", Name: "mac.local", Token: "node-token"})
	if m := readMsg(node2); m.Type != "registered" {
		t.Fatalf("reconnect without the key: %+v", m)
	}
	client, _ := rig.dial(true)
	client.WriteJSON(&Message{Type: "execute", ID: "9", Target: "mac.local", Token: "node-token", Cmd: "hostname"})
	if m := readMsg(node2); m.Type != "command" || m.Cmd != "hostname" {
		t.Fatalf("the command did not reach the node: %+v", m)
	}
}

// The daemon puts its saved activation key into the register message (that is what lets it join a relay that has a secret).
func TestDaemonRegistersWithItsActivationKey(t *testing.T) {
	_, cleanup := setupTestConfig(t)
	defer cleanup()
	if err := saveActivationKey("KEY-XYZ"); err != nil {
		t.Fatal(err)
	}
	got := make(chan Message, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var reg Message
		c.ReadJSON(&reg)
		got <- reg
	}))
	defer srv.Close()
	td := &TargetDaemon{relayURL: "ws" + strings.TrimPrefix(srv.URL, "http"), name: "n", token: "tok"}
	go td.run()
	select {
	case reg := <-got:
		if reg.Type != "register" || reg.ActivationKey != "KEY-XYZ" {
			t.Errorf("register = %+v", reg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never registered")
	}
}
