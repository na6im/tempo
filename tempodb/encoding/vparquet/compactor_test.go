package vparquet

import (
	"context"
	"encoding/binary"
	"time"

	"testing"

	"github.com/go-kit/log"
	"github.com/google/uuid"
	"github.com/segmentio/parquet-go"

	tempo_io "github.com/grafana/tempo/pkg/io"
	"github.com/grafana/tempo/pkg/util/test"
	"github.com/grafana/tempo/tempodb/backend"
	"github.com/grafana/tempo/tempodb/backend/local"
	"github.com/grafana/tempo/tempodb/encoding/common"
	"github.com/stretchr/testify/require"
)

func BenchmarkCompactor(b *testing.B) {
	b.Run("Small", func(b *testing.B) {
		benchmarkCompactor(b, 1000, 100, 100) // 10M spans
	})
	b.Run("Medium", func(b *testing.B) {
		benchmarkCompactor(b, 100, 100, 1000) // 10M spans
	})
	b.Run("Large", func(b *testing.B) {
		benchmarkCompactor(b, 10, 1000, 1000) // 10M spans
	})
}

func benchmarkCompactor(b *testing.B, traceCount, batchCount, spanCount int) {
	rawR, rawW, _, err := local.New(&local.Config{
		Path: b.TempDir(),
	})
	require.NoError(b, err)

	r := backend.NewReader(rawR)
	w := backend.NewWriter(rawW)
	ctx := context.Background()
	l := log.NewNopLogger()

	cfg := &common.BlockConfig{
		BloomFP:             0.01,
		BloomShardSizeBytes: 100 * 1024,
		RowGroupSizeBytes:   20_000_000,
	}

	meta := createTestBlock(b, ctx, cfg, r, w, traceCount, batchCount, spanCount)

	inputs := []*backend.BlockMeta{meta}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewCompactor(common.CompactionOptions{
			BlockConfig:      *cfg,
			OutputBlocks:     1,
			FlushSizeBytes:   30_000_000,
			MaxBytesPerTrace: 50_000_000,
		})

		_, err = c.Compact(ctx, l, r, func(*backend.BlockMeta, time.Time) backend.Writer { return w }, inputs)
		require.NoError(b, err)
	}
}

func BenchmarkCompactorDupes(b *testing.B) {
	rawR, rawW, _, err := local.New(&local.Config{
		Path: b.TempDir(),
	})
	require.NoError(b, err)

	r := backend.NewReader(rawR)
	w := backend.NewWriter(rawW)
	ctx := context.Background()
	l := log.NewNopLogger()

	cfg := &common.BlockConfig{
		BloomFP:             0.01,
		BloomShardSizeBytes: 100 * 1024,
		RowGroupSizeBytes:   20_000_000,
	}

	// 1M span traces
	meta := createTestBlock(b, ctx, cfg, r, w, 10, 1000, 1000)
	inputs := []*backend.BlockMeta{meta, meta}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := NewCompactor(common.CompactionOptions{
			BlockConfig:      *cfg,
			OutputBlocks:     1,
			FlushSizeBytes:   30_000_000,
			MaxBytesPerTrace: 50_000_000,
			ObjectsCombined:  func(compactionLevel, objects int) {},
			SpansDiscarded:   func(spans int) {},
		})

		_, err = c.Compact(ctx, l, r, func(*backend.BlockMeta, time.Time) backend.Writer { return w }, inputs)
		require.NoError(b, err)
	}
}

// createTestBlock with the number of given traces and the needed sizes.
// Trace IDs are guaranteed to be monotonically increasing so that
// the block will be iterated in order.
// nolint: revive
func createTestBlock(t testing.TB, ctx context.Context, cfg *common.BlockConfig, r backend.Reader, w backend.Writer, traceCount, batchCount, spanCount int) *backend.BlockMeta {
	inMeta := &backend.BlockMeta{
		TenantID:     tenantID,
		BlockID:      uuid.New(),
		TotalObjects: traceCount,
	}

	sb := newStreamingBlock(ctx, cfg, inMeta, r, w, tempo_io.NewBufferedWriter)

	for i := 0; i < traceCount; i++ {
		id := make([]byte, 16)
		binary.LittleEndian.PutUint64(id, uint64(i))

		tr := test.MakeTraceWithSpanCount(batchCount, spanCount, id)
		trp := traceToParquet(id, tr, nil)

		err := sb.Add(trp, 0, 0)
		require.NoError(t, err)
		if sb.EstimatedBufferedBytes() > 20_000_000 {
			_, err := sb.Flush()
			require.NoError(t, err)
		}
	}

	_, err := sb.Complete()
	require.NoError(t, err)

	return sb.meta
}

func TestValueAlloc(t *testing.T) {
	_ = make([]parquet.Value, 1_000_000)
}
