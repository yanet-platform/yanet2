package metrics

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/modules/metrics/adapter"
	"github.com/yanet-platform/yanet2/modules/metrics/config"
	"github.com/yanet-platform/yanet2/modules/metrics/controller"
	"github.com/yanet-platform/yanet2/modules/metrics/format"
	"github.com/yanet-platform/yanet2/modules/metrics/format/prometheues"
	grpchandler "github.com/yanet-platform/yanet2/modules/metrics/grpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.MustLoad("metrics.yaml")

	log, _, err := logging.Init(&cfg.Logging)
	if err != nil {
		fmt.Printf("failed to initialize logging: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grpcServer := grpc.NewServer()
	defer grpcServer.Stop()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatal("failed to listen", zap.Error(err))
	}

	log.Info("Starting metric adapter server", zap.Int("port", cfg.Port))

	typeFormat := format.Format(format.ConfigFormat(cfg.Format))

	if typeFormat == format.ConverterUndefined {
		log.Fatal("unsupported format type", zap.String("format", cfg.Format))
	}

	var builder prometheues.FormatBuilder = &strings.Builder{}

	formatter := format.NewFormatter(builder, typeFormat)
	if formatter == nil {
		log.Fatal("failed to create formatter", zap.String("format", cfg.Format))
	}

	clientConn, err := grpc.NewClient(cfg.ModulesAdress)
	if err != nil {
		log.Fatal("failed to connect to modules", zap.Error(err))
	}
	defer clientConn.Close()

	collector := adapter.NewCollector(clientConn, cfg.Modules)

	ctrl := controller.NewContoller(formatter, collector)

	grpchandler.Register(grpcServer, ctrl)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Error("failed to serve", zap.Error(err))
		}
	}()

	log.Info("Metric adapter server started successfully")

	<-ctx.Done()
	log.Info("Shutting down metric adapter server")
}
