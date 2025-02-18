// Copyright 2019 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cockroachdb/datadriven"
	"github.com/cockroachdb/pebble/internal/humanize"
	"github.com/cockroachdb/pebble/internal/testkeys"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/cockroachdb/redact"
	"github.com/stretchr/testify/require"
)

func TestMetricsFormat(t *testing.T) {
	var m Metrics
	m.BlockCache.Size = 1
	m.BlockCache.Count = 2
	m.BlockCache.Hits = 3
	m.BlockCache.Misses = 4
	m.Compact.Count = 5
	m.Compact.DefaultCount = 27
	m.Compact.DeleteOnlyCount = 28
	m.Compact.ElisionOnlyCount = 29
	m.Compact.MoveCount = 30
	m.Compact.ReadCount = 31
	m.Compact.RewriteCount = 32
	m.Compact.MultiLevelCount = 33
	m.Compact.EstimatedDebt = 6
	m.Compact.InProgressBytes = 7
	m.Compact.NumInProgress = 2
	m.Flush.Count = 8
	m.Flush.AsIngestBytes = 34
	m.Flush.AsIngestTableCount = 35
	m.Flush.AsIngestCount = 36
	m.Filter.Hits = 9
	m.Filter.Misses = 10
	m.MemTable.Size = 11
	m.MemTable.Count = 12
	m.MemTable.ZombieSize = 13
	m.MemTable.ZombieCount = 14
	m.Snapshots.Count = 4
	m.Snapshots.EarliestSeqNum = 1024
	m.Table.ZombieSize = 15
	m.Table.ZombieCount = 16
	m.TableCache.Size = 17
	m.TableCache.Count = 18
	m.TableCache.Hits = 19
	m.TableCache.Misses = 20
	m.TableIters = 21
	m.WAL.Files = 22
	m.WAL.ObsoleteFiles = 23
	m.WAL.Size = 24
	m.WAL.BytesIn = 25
	m.WAL.BytesWritten = 26
	m.Ingest.Count = 27

	for i := range m.Levels {
		l := &m.Levels[i]
		base := uint64((i + 1) * 100)
		l.Sublevels = int32(i + 1)
		l.NumFiles = int64(base) + 1
		l.Size = int64(base) + 2
		l.Score = float64(base) + 3
		l.BytesIn = base + 4
		l.BytesIngested = base + 4
		l.BytesMoved = base + 6
		l.BytesRead = base + 7
		l.BytesCompacted = base + 8
		l.BytesFlushed = base + 9
		l.TablesCompacted = base + 10
		l.TablesFlushed = base + 11
		l.TablesIngested = base + 12
		l.TablesMoved = base + 13
	}

	const expected = `
__level_____count____size___score______in__ingest(sz_cnt)____move(sz_cnt)___write(sz_cnt)____read___r-amp___w-amp
    WAL        22    24 B       -    25 B       -       -       -       -    26 B       -       -       -     1.0
      0       101   102 B  103.00   104 B   104 B     112   106 B     113   217 B     221   107 B       1     2.1
      1       201   202 B  203.00   204 B   204 B     212   206 B     213   417 B     421   207 B       2     2.0
      2       301   302 B  303.00   304 B   304 B     312   306 B     313   617 B     621   307 B       3     2.0
      3       401   402 B  403.00   404 B   404 B     412   406 B     413   817 B     821   407 B       4     2.0
      4       501   502 B  503.00   504 B   504 B     512   506 B     513  1017 B   1.0 K   507 B       5     2.0
      5       601   602 B  603.00   604 B   604 B     612   606 B     613   1.2 K   1.2 K   607 B       6     2.0
      6       701   702 B       -   704 B   704 B     712   706 B     713   1.4 K   1.4 K   707 B       7     2.0
  total      2807   2.7 K       -   2.8 K   2.8 K   2.9 K   2.8 K   2.9 K   8.4 K   5.7 K   2.8 K      28     3.0
  flush         8                            34 B      35      36  (ingest = tables-ingested, move = ingested-as-flushable)
compact         5     6 B     7 B       2                          (size == estimated-debt, score = in-progress-bytes, in = num-in-progress)
  ctype        27      28      29      30      31      32      33  (default, delete, elision, move, read, rewrite, multi-level)
 memtbl        12    11 B
zmemtbl        14    13 B
   ztbl        16    15 B
 bcache         2     1 B   42.9%  (score == hit-rate)
 tcache        18    17 B   48.7%  (score == hit-rate)
  snaps         4       -    1024  (score == earliest seq num)
 titers        21
 filter         -       -   47.4%  (score == utility)
 ingest        27
`
	if s := "\n" + m.String(); expected != s {
		t.Fatalf("expected%s\nbut found%s", expected, s)
	}
}

func TestMetrics(t *testing.T) {
	opts := &Options{
		Comparer:              testkeys.Comparer,
		FormatMajorVersion:    FormatNewest,
		FS:                    vfs.NewMem(),
		L0CompactionThreshold: 8,
	}
	opts.Experimental.EnableValueBlocks = func() bool { return true }
	opts.Levels = append(opts.Levels, LevelOptions{TargetFileSize: 50})

	// Prevent foreground flushes and compactions from triggering asynchronous
	// follow-up compactions. This avoids asynchronously-scheduled work from
	// interfering with the expected metrics output and reduces test flakiness.
	opts.DisableAutomaticCompactions = true

	// Increase the threshold for memtable stalls to allow for more flushable
	// ingests.
	opts.MemTableStopWritesThreshold = 4

	d, err := Open("", opts)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, d.Close())
	}()

	iters := make(map[string]*Iterator)
	defer func() {
		for _, i := range iters {
			require.NoError(t, i.Close())
		}
	}()

	datadriven.RunTest(t, "testdata/metrics", func(t *testing.T, td *datadriven.TestData) string {
		switch td.Cmd {
		case "batch":
			b := d.NewBatch()
			if err := runBatchDefineCmd(td, b); err != nil {
				return err.Error()
			}
			b.Commit(nil)
			return ""

		case "build":
			if err := runBuildCmd(td, d, d.opts.FS); err != nil {
				return err.Error()
			}
			return ""

		case "compact":
			if err := runCompactCmd(td, d); err != nil {
				return err.Error()
			}

			d.mu.Lock()
			s := d.mu.versions.currentVersion().String()
			d.mu.Unlock()
			return s

		case "delay-flush":
			d.mu.Lock()
			defer d.mu.Unlock()
			switch td.Input {
			case "enable":
				d.mu.compact.flushing = true
			case "disable":
				d.mu.compact.flushing = false
			default:
				return fmt.Sprintf("unknown directive %q (expected 'enable'/'disable')", td.Input)
			}
			return ""

		case "flush":
			if err := d.Flush(); err != nil {
				return err.Error()
			}

			d.mu.Lock()
			s := d.mu.versions.currentVersion().String()
			d.mu.Unlock()
			return s

		case "ingest":
			if err := runIngestCmd(td, d, d.opts.FS); err != nil {
				return err.Error()
			}
			return ""

		case "iter-close":
			if len(td.CmdArgs) != 1 {
				return "iter-close <name>"
			}
			name := td.CmdArgs[0].String()
			if iter := iters[name]; iter != nil {
				if err := iter.Close(); err != nil {
					return err.Error()
				}
				delete(iters, name)
			} else {
				return fmt.Sprintf("%s: not found", name)
			}

			// The deletion of obsolete files happens asynchronously when an iterator
			// is closed. Wait for the obsolete tables to be deleted.
			d.cleanupManager.Wait()
			return ""

		case "iter-new":
			if len(td.CmdArgs) != 1 {
				return "iter-new <name>"
			}
			name := td.CmdArgs[0].String()
			if iter := iters[name]; iter != nil {
				if err := iter.Close(); err != nil {
					return err.Error()
				}
			}
			iter := d.NewIter(nil)
			// Some iterators (eg. levelIter) do not instantiate the underlying
			// iterator until the first positioning call. Position the iterator
			// so that levelIters will have loaded an sstable.
			iter.First()
			iters[name] = iter
			return ""

		case "metrics":
			// The asynchronous loading of table stats can change metrics, so
			// wait for all the tables' stats to be loaded.
			d.mu.Lock()
			d.waitTableStats()
			d.mu.Unlock()

			return d.Metrics().String()

		case "disk-usage":
			return humanize.IEC.Uint64(d.Metrics().DiskSpaceUsage()).String()

		case "additional-metrics":
			// The asynchronous loading of table stats can change metrics, so
			// wait for all the tables' stats to be loaded.
			d.mu.Lock()
			d.waitTableStats()
			d.mu.Unlock()

			m := d.Metrics()
			var b strings.Builder
			fmt.Fprintf(&b, "block bytes written:\n")
			fmt.Fprintf(&b, " __level___data-block__value-block\n")
			for i := range m.Levels {
				fmt.Fprintf(&b, "%7d ", i)
				fmt.Fprintf(&b, "%12s %12s\n",
					humanize.IEC.Uint64(m.Levels[i].Additional.BytesWrittenDataBlocks),
					humanize.IEC.Uint64(m.Levels[i].Additional.BytesWrittenValueBlocks))
			}
			return b.String()

		default:
			return fmt.Sprintf("unknown command: %s", td.Cmd)
		}
	})
}

func TestMetricsRedact(t *testing.T) {
	const expected = `
__level_____count____size___score______in__ingest(sz_cnt)____move(sz_cnt)___write(sz_cnt)____read___r-amp___w-amp
    WAL         0     0 B       -     0 B       -       -       -       -     0 B       -       -       -     0.0
      0         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      1         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      2         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      3         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      4         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      5         0     0 B    0.00     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
      6         0     0 B       -     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
  total         0     0 B       -     0 B     0 B       0     0 B       0     0 B       0     0 B       0     0.0
  flush         0                             0 B       0       0  (ingest = tables-ingested, move = ingested-as-flushable)
compact         0     0 B     0 B       0                          (size == estimated-debt, score = in-progress-bytes, in = num-in-progress)
  ctype         0       0       0       0       0       0       0  (default, delete, elision, move, read, rewrite, multi-level)
 memtbl         0     0 B
zmemtbl         0     0 B
   ztbl         0     0 B
 bcache         0     0 B    0.0%  (score == hit-rate)
 tcache         0     0 B    0.0%  (score == hit-rate)
  snaps         0       -       0  (score == earliest seq num)
 titers         0
 filter         -       -    0.0%  (score == utility)
 ingest         0
`

	got := redact.Sprintf("%s", &Metrics{}).Redact()
	if s := "\n" + got; expected != s {
		t.Fatalf("expected%s\nbut found%s", expected, s)
	}
}

func TestMetricsWAmpDisableWAL(t *testing.T) {
	d, err := Open("", &Options{FS: vfs.NewMem(), DisableWAL: true})
	require.NoError(t, err)
	ks := testkeys.Alpha(2)
	wo := WriteOptions{Sync: false}
	for i := 0; i < 5; i++ {
		v := []byte(strconv.Itoa(i))
		for j := 0; j < ks.Count(); j++ {
			require.NoError(t, d.Set(testkeys.Key(ks, j), v, &wo))
		}
		require.NoError(t, d.Flush())
		require.NoError(t, d.Compact([]byte("a"), []byte("z"), false /* parallelize */))
	}
	m := d.Metrics()
	tot := m.Total()
	require.Greater(t, tot.WriteAmp(), 1.0)
	require.NoError(t, d.Close())
}
