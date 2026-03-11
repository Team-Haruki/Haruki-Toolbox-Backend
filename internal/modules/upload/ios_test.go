package upload

import (
	harukiUtils "haruki-suite/utils"
	"testing"
	"time"
)

func TestValidateDataUploadHeader(t *testing.T) {
	t.Parallel()

	valid := &dataUploadHeader{
		UploadId:    "u-123",
		ChunkIndex:  0,
		TotalChunks: 1,
	}
	if err := validateDataUploadHeader(valid); err != nil {
		t.Fatalf("validateDataUploadHeader(valid) returned error: %v", err)
	}

	tests := []struct {
		name   string
		header dataUploadHeader
	}{
		{
			name: "missing upload id",
			header: dataUploadHeader{
				UploadId:    " ",
				ChunkIndex:  0,
				TotalChunks: 1,
			},
		},
		{
			name: "upload id too long",
			header: dataUploadHeader{
				UploadId:    string(make([]byte, maxUploadIDLength+1)),
				ChunkIndex:  0,
				TotalChunks: 1,
			},
		},
		{
			name: "total chunks too small",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  0,
				TotalChunks: 0,
			},
		},
		{
			name: "total chunks too large",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  0,
				TotalChunks: maxUploadChunkCount + 1,
			},
		},
		{
			name: "negative chunk index",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  -1,
				TotalChunks: 2,
			},
		},
		{
			name: "chunk index out of range",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  2,
				TotalChunks: 2,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateDataUploadHeader(&tc.header); err == nil {
				t.Fatalf("validateDataUploadHeader(%s) should fail", tc.name)
			}
		})
	}
}

func TestBuildChunkUploadKey(t *testing.T) {
	t.Parallel()

	key := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")
	if key != "toolbox-user|jp|123456|upload-id" {
		t.Fatalf("unexpected upload key: %q", key)
	}
}

func TestHasAllChunks(t *testing.T) {
	t.Parallel()

	all := []harukiUtils.DataChunk{
		{ChunkIndex: 2},
		{ChunkIndex: 0},
		{ChunkIndex: 1},
	}
	if !hasAllChunks(all, 3) {
		t.Fatalf("hasAllChunks should return true when all chunk indices are present")
	}

	missing := []harukiUtils.DataChunk{
		{ChunkIndex: 0},
		{ChunkIndex: 2},
	}
	if hasAllChunks(missing, 3) {
		t.Fatalf("hasAllChunks should return false when chunks are missing")
	}

	duplicate := []harukiUtils.DataChunk{
		{ChunkIndex: 0},
		{ChunkIndex: 0},
		{ChunkIndex: 2},
	}
	if hasAllChunks(duplicate, 3) {
		t.Fatalf("hasAllChunks should return false when chunk indices are duplicated")
	}

	outOfRange := []harukiUtils.DataChunk{
		{ChunkIndex: 0},
		{ChunkIndex: 1},
		{ChunkIndex: 3},
	}
	if hasAllChunks(outOfRange, 3) {
		t.Fatalf("hasAllChunks should return false when a chunk index is out of range")
	}
}

func TestCleanExpiredChunks(t *testing.T) {
	backupChunks, backupTotals, backupSize := snapshotChunkStoreState()
	defer restoreChunkStoreState(backupChunks, backupTotals, backupSize)

	now := time.Now()
	setChunkStoreState(
		map[string][]harukiUtils.DataChunk{
			"expired": {
				{
					ChunkIndex: 0,
					Time:       now.Add(-6 * time.Minute),
					Data:       []byte{1, 2},
				},
			},
			"active": {
				{
					ChunkIndex: 0,
					Time:       now.Add(-1 * time.Minute),
					Data:       []byte{3},
				},
			},
			"empty": {},
		},
		map[string]int{
			"expired": 1,
			"active":  1,
			"empty":   1,
		},
		3,
	)

	cleanExpiredChunks()

	dataChunksMutex.RLock()
	defer dataChunksMutex.RUnlock()

	if _, ok := dataChunks["expired"]; ok {
		t.Fatalf("expired chunk set should be removed")
	}
	if _, ok := dataChunks["empty"]; ok {
		t.Fatalf("empty chunk set should be removed")
	}
	if _, ok := dataChunks["active"]; !ok {
		t.Fatalf("active chunk set should remain")
	}
	if _, ok := dataChunkTotals["active"]; !ok {
		t.Fatalf("active total should remain")
	}
	if dataChunksSize != 1 {
		t.Fatalf("dataChunksSize = %d, want 1", dataChunksSize)
	}
}

func snapshotChunkStoreState() (map[string][]harukiUtils.DataChunk, map[string]int, int64) {
	dataChunksMutex.RLock()
	defer dataChunksMutex.RUnlock()

	chunksCopy := make(map[string][]harukiUtils.DataChunk, len(dataChunks))
	for key, chunks := range dataChunks {
		copied := make([]harukiUtils.DataChunk, len(chunks))
		for i := range chunks {
			copied[i] = chunks[i]
			if chunks[i].Data != nil {
				copiedData := make([]byte, len(chunks[i].Data))
				copy(copiedData, chunks[i].Data)
				copied[i].Data = copiedData
			}
		}
		chunksCopy[key] = copied
	}

	totalsCopy := make(map[string]int, len(dataChunkTotals))
	for key, total := range dataChunkTotals {
		totalsCopy[key] = total
	}

	return chunksCopy, totalsCopy, dataChunksSize
}

func restoreChunkStoreState(chunks map[string][]harukiUtils.DataChunk, totals map[string]int, size int64) {
	dataChunksMutex.Lock()
	defer dataChunksMutex.Unlock()
	dataChunks = chunks
	dataChunkTotals = totals
	dataChunksSize = size
}

func setChunkStoreState(chunks map[string][]harukiUtils.DataChunk, totals map[string]int, size int64) {
	dataChunksMutex.Lock()
	defer dataChunksMutex.Unlock()
	dataChunks = chunks
	dataChunkTotals = totals
	dataChunksSize = size
}
