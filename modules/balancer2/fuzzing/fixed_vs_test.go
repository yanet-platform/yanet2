package fuzzing

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// vsKeyFor builds the canonical VS key a corpus address/port/proto triple
// resolves to. It mirrors the parser's 16-byte canonicalisation so tests
// can compare against parseFixedVS output without depending on it.
func vsKeyFor(t *testing.T, addr string, port uint16, proto balancerpb.TransportProto) VsKey {
	t.Helper()
	ip := net.ParseIP(addr)
	require.NotNil(t, ip, "addr %q must parse", addr)
	v16 := ip.To16()
	require.NotNil(t, v16, "addr %q must canonicalise to 16 bytes", addr)
	var key VsKey
	copy(key.IP[:], v16)
	key.Port = port
	key.Proto = proto
	return key
}

func TestParseFixedVS(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want VsKey
	}{
		{
			name: "ipv4 default proto",
			spec: "10.0.0.1:80",
			want: vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_TCP),
		},
		{
			name: "ipv4 udp",
			spec: "10.0.0.1:80/udp",
			want: vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_UDP),
		},
		{
			name: "ipv6 bracketed default proto",
			spec: "[2a02:6b8::242]:443",
			want: vsKeyFor(t, "2a02:6b8::242", 443, balancerpb.TransportProto_TCP),
		},
		{
			name: "ipv6 bracketed tcp",
			spec: "[2a02:6b8::242]:443/tcp",
			want: vsKeyFor(t, "2a02:6b8::242", 443, balancerpb.TransportProto_TCP),
		},
		{
			name: "surrounding whitespace",
			spec: "  10.0.0.1:80  ",
			want: vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_TCP),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFixedVS(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseFixedVSErrors(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		wantSubstr string
	}{
		{name: "empty", spec: "   ", wantSubstr: "empty"},
		{name: "missing port", spec: "10.0.0.1", wantSubstr: "invalid address"},
		{name: "bad ip", spec: "999.0.0.1:80", wantSubstr: "invalid IP literal"},
		{name: "unbracketed ipv6", spec: "2a02:6b8::242:443", wantSubstr: "invalid address"},
		{name: "non numeric port", spec: "10.0.0.1:http", wantSubstr: "invalid port"},
		{name: "zero port", spec: "10.0.0.1:0", wantSubstr: "out of range"},
		{name: "port too large", spec: "10.0.0.1:70000", wantSubstr: "out of range"},
		{name: "bad proto", spec: "10.0.0.1:80/sctp", wantSubstr: "unsupported protocol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFixedVS(tt.spec)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSubstr)
		})
	}
}

func TestRuntimeConfigFixedVSKeys(t *testing.T) {
	cfg := &RuntimeConfig{
		FixedVirtualServices: []string{"10.0.0.1:80", "[2a02:6b8::242]:443/tcp"},
	}
	keys, err := cfg.FixedVSKeys()
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_TCP), keys[0])
	assert.Equal(t, vsKeyFor(t, "2a02:6b8::242", 443, balancerpb.TransportProto_TCP), keys[1])

	cfg.FixedVirtualServices = []string{"not-an-address"}
	_, err = cfg.FixedVSKeys()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed_virtual_services")
}

func TestRuntimeConfigValidateRejectsBadFixedVS(t *testing.T) {
	in := validYAML + "fixed_virtual_services:\n  - \"bad-entry\"\n"
	_, err := DecodeRuntimeConfig([]byte(in))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed_virtual_services")
}

func TestLoadRuntimeConfigParsesFixedVS(t *testing.T) {
	in := validYAML + "fixed_virtual_services:\n  - \"10.0.0.1:80\"\n  - \"10.0.0.2:443/udp\"\n"
	cfg, err := DecodeRuntimeConfig([]byte(in))
	require.NoError(t, err)
	require.Equal(t, []string{"10.0.0.1:80", "10.0.0.2:443/udp"}, cfg.FixedVirtualServices)

	keys, err := cfg.FixedVSKeys()
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

func TestModelFixedVS(t *testing.T) {
	model := newModelFromText(t, genCorpusText(5, 4))
	order := model.OriginalOrder()
	require.Len(t, order, 5)

	fixed := order[0]
	other := order[1]
	model = NewModel(parsedCorpus(t, genCorpusText(5, 4)), WithFixedVS([]VsKey{fixed}))

	assert.True(t, model.IsFixed(fixed))
	assert.False(t, model.IsFixed(other))

	deletable := model.DeletableActiveOrder()
	assert.Len(t, deletable, 4, "the single fixed VS must be excluded")
	for _, key := range deletable {
		assert.NotEqual(t, fixed, key, "deletable set must never contain a fixed VS")
	}

	op := Operation{
		Type:     OpDeleteVS,
		DeleteVS: &DeleteVSPayload{Keys: []VsKey{fixed}},
	}
	err := model.Apply(op)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed")
	assert.True(t, model.IsActive(fixed), "a fixed VS must stay active after a rejected delete")
}

func TestModelDeletableActiveOrderNoFixed(t *testing.T) {
	model := newModelFromText(t, genCorpusText(3, 2))
	assert.Equal(t, model.ActiveOrder(), model.DeletableActiveOrder())
}

// TestOperationGeneratorNeverDeletesFixedVS drives the generator for many
// operations against a model with pinned VSes and asserts the fixed set is
// never removed and stays active throughout, while ordinary DeleteVS
// operations still fire on the remaining VSes.
func TestOperationGeneratorNeverDeletesFixedVS(t *testing.T) {
	const vsCount = 20
	const realsPerVS = 4
	const n = uint64(5)
	const steps = uint64(2000)

	text := genCorpusText(vsCount, realsPerVS)
	fixedKeys := []VsKey{
		vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_TCP),
		vsKeyFor(t, "10.0.0.2", 80, balancerpb.TransportProto_TCP),
		vsKeyFor(t, "10.0.0.3", 80, balancerpb.TransportProto_TCP),
	}
	model := NewModel(parsedCorpus(t, text), WithFixedVS(fixedKeys))
	gen := NewOperationGenerator(model, n, 12345)

	for _, key := range fixedKeys {
		require.True(t, model.IsFixed(key), "expected VS %v to be fixed", key)
	}

	sawRealDelete := false
	for opNum := uint64(1); opNum <= steps; opNum++ {
		op := gen.Generate(opNum)
		if op.Type == OpDeleteVS {
			require.NotEmpty(t, op.DeleteVS.Keys)
			sawRealDelete = true
			for _, key := range op.DeleteVS.Keys {
				assert.False(t, model.IsFixed(key),
					"op %d deletes fixed VS %v", opNum, key)
			}
		}
		require.NoError(t, model.Apply(op))

		for _, key := range fixedKeys {
			assert.True(t, model.IsActive(key),
				"op %d removed fixed VS %v from the active set", opNum, key)
		}
	}
	assert.True(t, sawRealDelete, "test must exercise at least one real DeleteVS")
}

func TestNewRunnerFixedVS(t *testing.T) {
	corpus := parsedCorpus(t, genCorpusText(5, 4))
	stats := NewLatencyStats()
	fake := newRunnerFakeRPC("fuzz-cfg")

	t.Run("rejects unknown fixed vs", func(t *testing.T) {
		cfg := runnerTestConfig()
		cfg.FixedVirtualServices = []string{"203.0.113.1:80"}
		_, err := NewRunner(cfg, corpus, fake, stats, WithSignals())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not defined in the corpus")
	})

	t.Run("accepts corpus fixed vs", func(t *testing.T) {
		cfg := runnerTestConfig()
		cfg.FixedVirtualServices = []string{"10.0.0.1:80"}
		runner, err := NewRunner(cfg, corpus, fake, stats, WithSignals())
		require.NoError(t, err)
		assert.True(t, runner.Model().IsFixed(
			vsKeyFor(t, "10.0.0.1", 80, balancerpb.TransportProto_TCP)))
	})
}

// parsedCorpus parses synthetic corpus text into a Corpus for tests that
// need to build a model with construction options.
func parsedCorpus(t *testing.T, text string) *Corpus {
	t.Helper()
	corpus, err := ParseServicesCorpusFromReader("synthetic.conf", strings.NewReader(text))
	require.NoError(t, err)
	return corpus
}
