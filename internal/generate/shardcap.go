package generate

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"sort"
	"time"
)

// ShardPart is one emitted shard file.
type ShardPart struct {
	Path  string
	Part  int
	Bytes []byte
	Count int
}

// EmitShardsForScopeMonth packs one (scope, published-month) bucket into 1..K
// parts obeying both caps. entriesInsertionOrder is the month's articles in
// insertion order (the seal axis); part k holds the k-th run of earliest-
// inserted entries. Each part's file content is written in display order
// (newest-published-first) so the client can concatenate parts to one
// newest-first stream. A tail insert extends the highest part or opens a new
// one; lower parts stay byte-stable. A back-dated insert re-cuts the bucket.
func EmitShardsForScopeMonth(scope string, y, m int, entriesInsertionOrder []ShardEntry, capE, capB int) ([]ShardPart, error) {
	var parts []ShardPart
	var cur []ShardEntry

	flush := func() error {
		if len(cur) == 0 {
			return nil
		}
		b, err := marshalShard(displaySorted(cur))
		if err != nil {
			return err
		}
		parts = append(parts, ShardPart{Part: len(parts) + 1, Bytes: b, Count: len(cur)})
		cur = nil
		return nil
	}

	for _, e := range entriesInsertionOrder {
		trial := append(append([]ShardEntry{}, cur...), e)
		over := len(trial) > capE
		if !over {
			// gzip byte check only when the entry cap is not already the trigger.
			over = gzipLen(mustMarshalShard(displaySorted(trial))) > capB
		}
		if over && len(cur) > 0 {
			if err := flush(); err != nil {
				return nil, err
			}
			cur = append(cur, e)
			continue
		}
		cur = trial
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for i := range parts {
		parts[i].Path = shardPartPath(scope, y, m, parts[i].Part)
	}
	return parts, nil
}

// displaySorted returns a copy sorted by the same display comparator used for
// HTML pages (newest-published-first, ties by id).
func displaySorted(in []ShardEntry) []ShardEntry {
	out := append([]ShardEntry{}, in...)
	sort.SliceStable(out, func(i, j int) bool { return entryLess(out[i], out[j]) })
	return out
}

// entryLess mirrors the server's portadaLess for ShardEntry: a newer published
// DAY first, then within a day the curated ord (0 = the day's lead), then newest
// ts, ties by id desc. Uncurated entries all carry the default ord 0, so a day
// falls straight through to the ts/id ordering the old comparator used, keeping
// uncurated shards byte-stable.
func entryLess(a, b ShardEntry) bool {
	da, db := shardDay(a), shardDay(b)
	if da != db {
		return a.TS > b.TS // newer published day first
	}
	if a.Ord != b.Ord {
		return a.Ord < b.Ord // within a day: curated order
	}
	if a.TS != b.TS {
		return a.TS > b.TS // default within-day: newest published first
	}
	return idLess(b.id, a.id)
}

// shardDay is the entry's published-day bucket ("YYYY-MM-DD") from its RFC3339
// published_at, matching the server's dayKey and the client's day grouping.
func shardDay(e ShardEntry) string {
	if len(e.PublishedAt) >= 10 {
		return e.PublishedAt[:10]
	}
	return e.PublishedAt
}

// gzipLen measures the gzipped size of b with a deterministic header (the length
// is host/version-stable). The result is only used to enforce the byte cap;
// shards are stored as plain JSON.
func gzipLen(b []byte) int {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.ModTime = time.Time{}
	zw.OS = 255
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Len()
}

func marshalShard(entries []ShardEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // standalone application/json file, not embedded in HTML
	if err := enc.Encode(entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mustMarshalShard(entries []ShardEntry) []byte {
	b, err := marshalShard(entries)
	if err != nil {
		panic(err)
	}
	return b
}
