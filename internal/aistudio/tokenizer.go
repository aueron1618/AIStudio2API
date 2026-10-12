package aistudio

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"sync"
	"time"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model.gz
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
	model, err := gzip.NewReader(bytes.NewReader(geminiTokenizerModel))
	if err != nil {
		panic(err)
	}
	defer model.Close()
	processor, err := sentencepiece.NewProcessor(model)
	if err != nil {
		panic(err)
	}
	return processor
}

func localTextTokens(value string) int64 {
	if value == "" {
		return 0
	}
	processor := acquireGeminiTokenizer()
	defer releaseGeminiTokenizer()
	return int64(len(processor.Encode(value)))
}
