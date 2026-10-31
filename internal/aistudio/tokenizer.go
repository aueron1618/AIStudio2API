package aistudio

import (
	"bytes"
	_ "embed"
	"hash/maphash"
	"sync"
	"time"

	sentencepiece "github.com/Mag1cFall/go-sentencepiece"
	"github.com/klauspost/compress/zstd"
)

//go:embed gemma3.model.zst
var geminiTokenizerModel []byte

// tokenizerCache 串行加载大模型，但计数期间不持锁；释放缓存不会影响已取得的引用。
type tokenizerCache struct {
	mu        sync.Mutex
	processor *sentencepiece.Processor
	lastUsed  time.Time
	users     int
}

var localTokenizer tokenizerCache

func acquireGeminiTokenizer() *sentencepiece.Processor {
	localTokenizer.mu.Lock()
	defer localTokenizer.mu.Unlock()
	if localTokenizer.processor == nil {
		localTokenizer.processor = loadGeminiTokenizer()
	}
	localTokenizer.lastUsed = time.Now()
	localTokenizer.users++
	return localTokenizer.processor
}

func releaseGeminiTokenizer() {
	localTokenizer.mu.Lock()
	defer localTokenizer.mu.Unlock()
	localTokenizer.users--
	localTokenizer.lastUsed = time.Now()
}

// ReleaseIdleTokenizer 解除空闲分词模型的强引用，由零热池模式的资源回收器调用。
func ReleaseIdleTokenizer(before time.Time) bool {
	return localTokenizer.releaseBefore(before)
}

func (cache *tokenizerCache) releaseBefore(before time.Time) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.processor == nil || cache.users != 0 || cache.lastUsed.After(before) {
		return false
	}
	cache.processor = nil
	return true
}

func loadGeminiTokenizer() *sentencepiece.Processor {
	decoder, _ := zstd.NewReader(nil)
	model, err := decoder.DecodeAll(geminiTokenizerModel, nil)
	decoder.Close()
	if err != nil {
		panic(err)
	}
	processor, err := sentencepiece.NewProcessor(bytes.NewReader(model))
	if err != nil {
		panic(err)
	}
	return processor
}

// tokenCountCacheMinBytes 是缓存本地 Token 数的最短文本字节数
const tokenCountCacheMinBytes = 1024

// tokenCountCacheLimit 是本地 Token 数缓存的条目上限，达到上限时清空
const tokenCountCacheLimit = 4096

// tokenCountCache 按文本的 maphash 缓存长文本的本地 Token 数
var tokenCountCache = struct {
	sync.Mutex
	seed   maphash.Seed
	counts map[uint64]int64
}{seed: maphash.MakeSeed(), counts: map[uint64]int64{}}

func localTextTokens(value string) int64 {
	if value == "" {
		return 0
	}
	var key uint64
	if len(value) >= tokenCountCacheMinBytes {
		key = maphash.String(tokenCountCache.seed, value)
		tokenCountCache.Lock()
		count, ok := tokenCountCache.counts[key]
		tokenCountCache.Unlock()
		if ok {
			return count
		}
	}
	processor := acquireGeminiTokenizer()
	defer releaseGeminiTokenizer()
	count := int64(len(processor.Encode(value)))
	if len(value) >= tokenCountCacheMinBytes {
		tokenCountCache.Lock()
		if len(tokenCountCache.counts) >= tokenCountCacheLimit {
			clear(tokenCountCache.counts)
		}
		tokenCountCache.counts[key] = count
		tokenCountCache.Unlock()
	}
	return count
}
