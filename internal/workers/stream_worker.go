package workers

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"go.uber.org/zap"
)

const (
	ephNTRIP = "ntrip://Dolin:SergeySGGABG12@products.igs-ip.net:2101/BCEP00BKG0"
	ssrNTRIP = "ntrip://Dolin:SergeySGGABG12@products.igs-ip.net:2101/SSRA00CNE1"
)

// StreamWorker запускает и поддерживает два глобальных процесса str2str:
//   - EPH-поток (эфемериды) → TCP-сервер на порту 9200
//   - SSR-поток (поправки)  → TCP-сервер на порту 9201
type StreamWorker struct {
	str2strPath string
	logger      *zap.SugaredLogger
}

// NewStreamWorker создаёт воркер; str2strBin — абсолютный путь к бинарю str2str
func NewStreamWorker(str2strBin string, logger *zap.SugaredLogger) *StreamWorker {
	return &StreamWorker{
		str2strPath: str2strBin,
		logger:      logger,
	}
}

// Start запускает оба потока в фоне; завершается при отмене ctx
func (sw *StreamWorker) Start(ctx context.Context) {
	go sw.runStream(ctx, "EPH", ephNTRIP, 9200)
	go sw.runStream(ctx, "SSR", ssrNTRIP, 9201)
}

func (sw *StreamWorker) runStream(ctx context.Context, name, ntripURL string, port int) {
	outPath := fmt.Sprintf("tcpsvr://:%d", port)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sw.logger.Infof("StreamWorker: starting %s stream → port %d", name, port)
		cmd := exec.CommandContext(ctx, sw.str2strPath, "-in", ntripURL, "-out", outPath)
		cmd.Stderr = &streamLogWriter{logger: sw.logger, name: name}

		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return // нормальное завершение
			}
			sw.logger.Warnf("StreamWorker: %s stream exited (%v), restart in 10s", name, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

type streamLogWriter struct {
	logger *zap.SugaredLogger
	name   string
}

func (w *streamLogWriter) Write(p []byte) (int, error) {
	if msg := string(p); msg != "" {
		w.logger.Debugf("str2str[%s]: %s", w.name, msg)
	}
	return len(p), nil
}
