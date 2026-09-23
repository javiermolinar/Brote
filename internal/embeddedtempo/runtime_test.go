package embeddedtempo

import (
	"testing"
	"time"
)

func TestPersistentProfile(t *testing.T) {
	cfg, err := configuration(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackendWorker.Compactor.BlockRetention != time.Duration(1<<63-1) {
		t.Fatal("maximum finite retention not configured")
	}
	if cfg.Overrides.Defaults.Global.MaxBytesPerTrace != 0 {
		t.Fatal("compaction can discard large traces")
	}
	if cfg.Querier.Worker.GRPCClientConfig.MaxSendMsgSize < 128<<20 || cfg.Server.GRPCServerMaxRecvMsgSize < 128<<20 || cfg.Server.GRPCServerMaxSendMsgSize < 128<<20 || cfg.LiveStoreClient.GRPCClientConfig.MaxRecvMsgSize < 128<<20 {
		t.Fatal("query transport cannot carry supported snapshots")
	}
	if cfg.Memory.AutoMemLimitEnabled {
		t.Fatal("host memory management changed")
	}
	if cfg.StorageConfig.Trace.Block.Version != "vParquet5" {
		t.Fatalf("unexpected block format %s", cfg.StorageConfig.Trace.Block.Version)
	}
	if cfg.InternalServer.Enable || cfg.LiveStore.Ring.KVStore.Store != "inmemory" || cfg.LiveStore.PartitionRing.KVStore.Store != "inmemory" {
		t.Fatal("unexpected internal server or network ring")
	}
}
