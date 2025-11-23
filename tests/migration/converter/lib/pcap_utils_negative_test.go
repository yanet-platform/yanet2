package lib

import "testing"

func TestAnalyzePcapFile_MissingFile(t *testing.T) {
	pa := NewPcapAnalyzer(false)
	if _, err := pa.AnalyzePcapFile("/no/such/file.pcap"); err == nil {
		t.Fatalf("expected error for missing pcap, got nil")
	}
}


