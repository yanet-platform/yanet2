package main

// Benchmark configuration structure defining test parameters including virtual service counts,
// real server distribution, protocol probabilities, packet generation settings, and worker configuration.

type BenchConfig struct {
	GreProb    float32 `yaml:"gre_prob"`
	FixMSSProb float32 `yaml:"fix_mss_prob"`
	PureL3Prob float32 `yaml:"pure_l3_prob"`
	OpsProb    float32 `yaml:"ops_prob"`

	RoundRobinProb float32 `yaml:"round_robin_prob"`

	TCPIPv4VS int `yaml:"tcp_ipv4_vs"`
	TCPIPv6VS int `yaml:"tcp_ipv6_vs"`
	UDPIPv4VS int `yaml:"udp_ipv4_vs"`
	UDPIPv6Vs int `yaml:"udp_ipv6_vs"`

	Ipv4Reals int `yaml:"ipv4_reals"`
	Ipv6Reals int `yaml:"ipv6_reals"`

	AllowedSrcPerVs int `yaml:"allowed_src_per_vs"`

	NewSessionProb float32 `yaml:"new_session_prob"`

	IcmpProb         float32 `yaml:"icmp_prob"`
	IcmpRedirectProb float32 `yaml:"icmp_redirect_prob"`

	BatchesPerWorker int `yaml:"batches_per_worker"`
	PacketsPerBatch  int `yaml:"packets_per_batch"`

	mss int `yaml:"mss"`

	Workers int `yaml:"workers"`

	SessionTableCapacity int `yaml:"session_table_capacity"`
}
