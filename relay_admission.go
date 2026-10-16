package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Admission: who may register a node on a relay that has a secret, without presenting the secret.
//
//  1. An exempt name (RELAY_SECRET_EXEMPT at start, plus exempt-nodes.json, which is re-read whenever it changes, so adding or removing a name needs no restart:
//     `relay add-exempt <name>`). Name only: whoever claims the name is admitted (an already-registered name still needs its token to be taken over).
//  2. A node the relay already knows: the same name AND the same token it was admitted with (known-nodes.json, token stored as a sha256). This is what lets a
//     node that joined with an activation key reconnect later, and it is stricter than an exempt name: the token is the proof.
//  3. A valid activation key in the register message (`relay add-key`): the node is admitted and remembered (2). Revoke it with `relay remove-key`
//     (new nodes) and `relay forget-node <name>` (a node already admitted).
//
// None of this lets an unauthenticated connection act as a client (execute, file transfer, tunnels, pair_listen): see clientOnlyMessages.

func exemptNodesPath() string { return filepath.Join(configDir(), "exempt-nodes.json") }
func knownNodesPath() string  { return filepath.Join(configDir(), "known-nodes.json") }

// fileStamp is what a stat-based reload compares.
type fileStamp struct {
	mod  int64
	size int64
	ok   bool
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: info.ModTime().UnixNano(), size: info.Size(), ok: true}
}

// exemptStore is the hot-reloaded list of exempt node names.
type exemptStore struct {
	mu    sync.Mutex
	names map[string]bool
	stamp fileStamp
	path  string
}

var exemptNodes = &exemptStore{}

func (s *exemptStore) reload() {
	st := stampOf(exemptNodesPath())
	if st == s.stamp && s.names != nil && s.path == exemptNodesPath() {
		return
	}
	names := map[string]bool{}
	if st.ok {
		if data, err := os.ReadFile(exemptNodesPath()); err == nil {
			var list []string
			if json.Unmarshal(data, &list) == nil {
				for _, n := range list {
					names[n] = true
				}
			}
		}
	}
	s.names, s.stamp, s.path = names, st, exemptNodesPath()
}

func (s *exemptStore) has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return s.names[name]
}

func (s *exemptStore) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func writeJSONFile(path string, v interface{}) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // a reader never sees a half-written file
}

func addExemptNode(name string) error {
	list := exemptNodes.list()
	for _, n := range list {
		if n == name {
			return nil
		}
	}
	return writeJSONFile(exemptNodesPath(), append(list, name))
}

func removeExemptNode(name string) error {
	out := []string{}
	for _, n := range exemptNodes.list() {
		if n != name {
			out = append(out, n)
		}
	}
	return writeJSONFile(exemptNodesPath(), out)
}

// knownStore remembers the (name, token) pairs the relay admitted with an activation key.
type knownStore struct {
	mu    sync.Mutex
	nodes map[string]string // name -> sha256(token), hex
	stamp fileStamp
	path  string
}

var knownNodes = &knownStore{}

func tokenDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *knownStore) reload() {
	st := stampOf(knownNodesPath())
	if st == s.stamp && s.nodes != nil && s.path == knownNodesPath() {
		return
	}
	nodes := map[string]string{}
	if st.ok {
		if data, err := os.ReadFile(knownNodesPath()); err == nil {
			_ = json.Unmarshal(data, &nodes)
		}
	}
	s.nodes, s.stamp, s.path = nodes, st, knownNodesPath()
}

// matches: the relay admitted this name with this very token before.
func (s *knownStore) matches(name, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	want, ok := s.nodes[name]
	return ok && subtle.ConstantTimeCompare([]byte(want), []byte(tokenDigest(token))) == 1
}

func (s *knownStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return len(s.nodes)
}

func (s *knownStore) remember(name, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	s.nodes[name] = tokenDigest(token)
	err := writeJSONFile(knownNodesPath(), s.nodes)
	s.stamp = stampOf(knownNodesPath())
	return err
}

func (s *knownStore) forget(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	if _, ok := s.nodes[name]; !ok {
		return false, nil
	}
	delete(s.nodes, name)
	err := writeJSONFile(knownNodesPath(), s.nodes)
	s.stamp = stampOf(knownNodesPath())
	return true, err
}

func (s *knownStore) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	out := make([]string, 0, len(s.nodes))
	for n := range s.nodes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isExempt: a name allowed to register without the secret (the start-up list or the hot file).
func (rs *RelayServer) isExempt(name string) bool {
	return rs.secretExempt[name] || exemptNodes.has(name)
}

// canSkipSecret: could ANY unauthenticated connection be admitted? If not, a connection without the secret is refused at the handshake.
func (rs *RelayServer) canSkipSecret() bool {
	return len(rs.secretExempt) > 0 || len(exemptNodes.list()) > 0 || len(activationKeys.list()) > 0 || knownNodes.count() > 0
}

// admitWithoutSecret decides a register message from a connection that did not present the relay secret. how names the rule for the log.
func (rs *RelayServer) admitWithoutSecret(msg *Message) (ok bool, how string) {
	if rs.isExempt(msg.Name) {
		return true, "exempt name"
	}
	if knownNodes.matches(msg.Name, msg.Token) {
		return true, "known node"
	}
	if msg.ActivationKey != "" && activationKeys.isValid(msg.ActivationKey) {
		if err := knownNodes.remember(msg.Name, msg.Token); err != nil {
			// admitted for this connection; it will need the key again after a reconnect if the file cannot be written
			return true, "activation key (could not remember the node: " + err.Error() + ")"
		}
		return true, "activation key"
	}
	return false, ""
}
