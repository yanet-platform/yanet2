package fuzzing

import (
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// minimalCorpus is a self-contained two-VS fixture exercising every
// supported construct: IPv4 and IPv6 VS addresses, mixed scheduler
// directives, multiple reals, a skipped health-check block, and a
// quorum_up directive whose quoted argument contains a '#' that must not
// trip the comment stripper.
const minimalCorpus = `# leading comment
virtual_server 2a02:6b8::1 80 {
        version 1
        protocol TCP
        quorum_up   "/etc/keepalived/quorum-handler2.sh up 2a02:6b8::1,80/TCP,b-100,1#tag"
        lvs_sched wrr
        real_server 2a02:6b8:c08::a 80 {
                weight 3
                HTTP_GET {
                        url {
                                path /ping
                                status_code 200
                        }
                }
        }
}

virtual_server 87.250.254.35 443 {
        protocol UDP
        lvs_sched wlc
        real_server 2a02:6b8:c08::b 443 {
                weight 1
                SSL_GET {
                        url { path /ping }
                }
        }
        real_server 87.250.250.250 443 {
                weight 7
        }
}
`

// addr16 returns the canonical 16-byte form of an IP literal; it panics
// on malformed input because every caller passes a static, known-good
// string.
func addr16(t *testing.T, s string) [16]byte {
	t.Helper()
	ip := net.ParseIP(s).To16()
	require.NotNil(t, ip, "addr16: %q", s)
	var out [16]byte
	copy(out[:], ip)
	return out
}

func TestParseServicesCorpus(t *testing.T) {
	corpus, err := ParseServicesCorpusFromReader("minimal.conf", strings.NewReader(minimalCorpus))
	require.NoError(t, err)
	require.NotNil(t, corpus)
	require.Len(t, corpus.VSs, 2)

	vs1 := corpus.VSs[0]
	assert.Equal(t, "2a02:6b8::1", vs1.AddrText)
	assert.Equal(t, uint16(80), vs1.Key.Port)
	assert.Equal(t, balancerpb.TransportProto_TCP, vs1.Key.Proto)
	assert.Equal(t, balancerpb.VsScheduler_WRR, vs1.Scheduler)
	assert.Equal(t, "minimal.conf", vs1.Source)
	require.Len(t, vs1.Reals, 1)
	assert.Equal(t, "2a02:6b8:c08::a", vs1.Reals[0].AddrText)
	assert.Equal(t, uint16(80), vs1.Reals[0].Key.Port)
	assert.Equal(t, uint32(3), vs1.Reals[0].Weight)

	vs2 := corpus.VSs[1]
	assert.Equal(t, "87.250.254.35", vs2.AddrText)
	assert.Equal(t, uint16(443), vs2.Key.Port)
	assert.Equal(t, balancerpb.TransportProto_UDP, vs2.Key.Proto)
	assert.Equal(t, balancerpb.VsScheduler_WLC, vs2.Scheduler)
	require.Len(t, vs2.Reals, 2)
	assert.Equal(t, uint32(1), vs2.Reals[0].Weight)
	assert.Equal(t, uint32(7), vs2.Reals[1].Weight)

	// Lookup by canonical key must succeed and return the same pointer
	// as the slice entry so downstream code can mutate one and observe
	// the change through the other.
	got := corpus.Lookup(VsKey{
		IP:    addr16(t, "2a02:6b8::1"),
		Port:  80,
		Proto: balancerpb.TransportProto_TCP,
	})
	require.Same(t, vs1, got)

	// ToVsConfigList must preserve VS order and translate the parsed
	// scheduler/proto to the balancerpb enums.
	list := corpus.ToVsConfigList()
	require.Len(t, list.Vs, 2)
	assert.Equal(t, balancerpb.VsScheduler_WRR, list.Vs[0].Scheduler)
	assert.Equal(t, balancerpb.TransportProto_TCP, list.Vs[0].Id.Proto)
	assert.Equal(t, uint32(80), list.Vs[0].Id.Port)
	require.Len(t, list.Vs[0].Reals, 1)
	assert.Equal(t, uint32(3), *list.Vs[0].Reals[0].Weight)
	assert.Equal(t, uint32(80), list.Vs[0].Reals[0].Id.Port)
}

func TestParseExistingCorpora(t *testing.T) {
	corpus, err := ParseServicesCorpus(
		"taxi.services.conf",
	)
	require.NoError(t, err)
	require.NotNil(t, corpus)

	assert.NotEmpty(t, corpus.VSs, "merged corpus must expose at least one VS")
	totalReals := 0
	tcpCount := 0
	for _, vs := range corpus.VSs {
		assert.NotEmpty(
			t,
			vs.Reals,
			"vs %s:%d must have at least one real",
			vs.AddrText,
			vs.Key.Port,
		)
		totalReals += len(vs.Reals)
		assert.NotZero(t, vs.Key.Port, "port must be non-zero")
		assert.Contains(
			t,
			[]balancerpb.TransportProto{
				balancerpb.TransportProto_TCP,
				balancerpb.TransportProto_UDP,
			},
			vs.Key.Proto,
			"proto must be TCP or UDP",
		)
		if vs.Key.Proto == balancerpb.TransportProto_TCP {
			tcpCount++
		}
		assert.Contains(t,
			[]balancerpb.VsScheduler{
				balancerpb.VsScheduler_WRR,
				balancerpb.VsScheduler_WLC,
				balancerpb.VsScheduler_SH,
				balancerpb.VsScheduler_OP,
			},
			vs.Scheduler,
			"scheduler must be one of WRR/WLC/SH/OP")
	}
	assert.NotZero(t, totalReals)
	// The repo corpora are predominantly TCP; sanity-check that the
	// majority of parsed VSs land on the TCP enum so a future bug that
	// flips proto detection would still trip this assertion.
	assert.Greater(t, tcpCount, len(corpus.VSs)/2)

	// Spot-check a known fixture entry from taxi.services.conf.
	got := corpus.Lookup(VsKey{
		IP:    addr16(t, "2a02:6b8:0:3400:0:fa99:0:185"),
		Port:  80,
		Proto: balancerpb.TransportProto_TCP,
	})
	require.NotNil(t, got, "expected taxi VS to be present")
	assert.NotEmpty(t, got.Reals)
}

func TestParseRejectsDuplicateVS(t *testing.T) {
	const dup = `virtual_server 10.0.0.1 80 {
        protocol TCP
        lvs_sched wrr
        real_server 10.0.0.2 80 { weight 1 }
}
virtual_server 10.0.0.1 80 {
        protocol TCP
        lvs_sched wrr
        real_server 10.0.0.3 80 { weight 1 }
}
`
	_, err := ParseServicesCorpusFromReader("dup.conf", strings.NewReader(dup))
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "duplicate virtual_server")
	assert.Contains(t, msg, "10.0.0.1")
	// Error must carry the line number of the offending re-declaration so
	// operators can grep straight to the second block.
	assert.Contains(t, msg, `"dup.conf":6`)
	assert.Contains(t, msg, "dup.conf:1")
}

// TestParseRejectsInvalidInputs is table-driven and covers every error
// path documented in the task: invalid IP/port/proto, duplicate VS,
// duplicate real under one VS, unknown scheduler, unclosed brace, empty
// corpus, and a VS with zero reals.
func TestParseRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSubstr []string
	}{
		{
			name: "invalid ip",
			input: `virtual_server not-an-ip 80 {
        real_server 10.0.0.1 80 { weight 1 }
}
`,
			wantSubstr: []string{"invalid IP", "not-an-ip"},
		},
		{
			name: "invalid port",
			input: `virtual_server 10.0.0.1 70000 {
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"port", "70000"},
		},
		{
			name: "zero port",
			input: `virtual_server 10.0.0.1 0 {
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"port", "out of range"},
		},
		{
			name: "negative port",
			input: `virtual_server 10.0.0.1 -1 {
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"invalid port"},
		},
		{
			name: "invalid protocol",
			input: `virtual_server 10.0.0.1 80 {
        protocol SCTP
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"protocol", "SCTP"},
		},
		{
			name: "unknown scheduler",
			input: `virtual_server 10.0.0.1 80 {
        lvs_sched lc
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"lvs_sched", "lc"},
		},
		{
			name: "duplicate real",
			input: `virtual_server 10.0.0.1 80 {
        real_server 10.0.0.2 80 { weight 1 }
        real_server 10.0.0.2 80 { weight 2 }
}
`,
			wantSubstr: []string{"duplicate real_server", "10.0.0.2"},
		},
		{
			name: "vs without reals",
			input: `virtual_server 10.0.0.1 80 {
        protocol TCP
}
`,
			wantSubstr: []string{"zero real_servers", "10.0.0.1"},
		},
		{
			name: "unclosed vs brace",
			input: `virtual_server 10.0.0.1 80 {
        real_server 10.0.0.2 80 { weight 1 }
`,
			wantSubstr: []string{"unclosed brace"},
		},
		{
			name:       "empty corpus",
			input:      "# only a comment\n\n",
			wantSubstr: []string{"no virtual_server blocks"},
		},
		{
			name: "invalid weight",
			input: `virtual_server 10.0.0.1 80 {
        real_server 10.0.0.2 80 { weight abc }
}
`,
			wantSubstr: []string{"weight", "abc"},
		},
		{
			name: "stray closing brace",
			input: `}
virtual_server 10.0.0.1 80 {
        real_server 10.0.0.2 80 { weight 1 }
}
`,
			wantSubstr: []string{"unexpected '}'", "top level"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseServicesCorpusFromReader(tt.name+".conf", strings.NewReader(tt.input))
			require.Error(t, err)
			for _, want := range tt.wantSubstr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestParseDuplicateAcrossFiles(t *testing.T) {
	const a = `virtual_server 10.0.0.1 80 {
        real_server 10.0.0.2 80 { weight 1 }
}
`
	const b = `virtual_server 10.0.0.1 80 {
        real_server 10.0.0.3 80 { weight 1 }
}
`
	corpus := newCorpus()
	require.NoError(t, corpus.appendReader("a.conf", strings.NewReader(a)))
	err := corpus.appendReader("b.conf", strings.NewReader(b))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate virtual_server")
	assert.Contains(t, err.Error(), "a.conf:1")
}

func TestStripCommentPreservesQuotedHash(t *testing.T) {
	const in = `quorum_up "addr,80/TCP#tag" # trailing`
	got := stripComment(in)
	assert.Equal(t, `quorum_up "addr,80/TCP#tag" `, got)
}
