package operatorwait

import (
	"fmt"
	"strings"
	"time"
)

const birdAdapterReadyCommand = "for attempt in $(seq 1 60); do (: > /dev/tcp/127.0.0.1/50051) 2>/dev/null && exit 0; test \"$attempt\" -eq 60 || sleep 1; done; exit 1"

// BirdAdapter waits for the adapter listener and includes bounded diagnostics.
func BirdAdapter(run func(string, time.Duration) (string, error)) error {
	quotedCommand := "'" + strings.ReplaceAll(birdAdapterReadyCommand, "'", "'\"'\"'") + "'"
	if _, err := run("bash -c "+quotedCommand, 65*time.Second); err != nil {
		logs, logsErr := run("tail -c 8192 /tmp/yanet/logs/yanet-bird-adapter.log", 10*time.Second)
		logs = truncate(logs)
		if logsErr != nil {
			logs = fmt.Sprintf("adapter diagnostics unavailable: %v\n%s", logsErr, logs)
		}
		return fmt.Errorf("wait for BIRD adapter listener: %w\n%s", err, logs)
	}
	return nil
}

func truncate(output string) string {
	const limit = 8 << 10
	if len(output) <= limit {
		return output
	}
	return output[:limit] + fmt.Sprintf("\n... truncated (%d bytes total)", len(output))
}
