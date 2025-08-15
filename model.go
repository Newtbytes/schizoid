package main

import "math/rand/v2"

type Tokenizer interface {
	Encode(text string) []uint8
	Decode(tokens []uint8) string
	VocabSize() int
}

type CharTokenizer struct{}

func (c *CharTokenizer) Encode(text string) []uint8 {
	var tokens []uint8
	for _, char := range text {
		tokens = append(tokens, uint8(char))
	}
	return tokens
}

func (c *CharTokenizer) Decode(tokens []uint8) string {
	var text string
	for _, token := range tokens {
		text += string(byte(token))
	}
	return text
}

func (c *CharTokenizer) VocabSize() int {
	// ASCII
	return 256
}

type NgramModel struct {
	Counts map[string]uint64

	tokenizer Tokenizer
	N         int
	Smoothing float64
}

func NewNgramModel(tokenizer Tokenizer, n int, smoothing float64) *NgramModel {
	model := &NgramModel{
		Counts:    make(map[string]uint64),
		tokenizer: tokenizer,
		N:         n,
		Smoothing: smoothing,
	}

	return model
}

func ngrams(tokens []uint8, n int) [][]uint8 {
	var ngrams [][]uint8

	if n > len(tokens) || n <= 0 {
		return ngrams
	}

	for i := 0; i <= len(tokens)-n; i++ {
		ngrams = append(ngrams, tokens[i:i+n])
	}

	return ngrams
}

func (m *NgramModel) train(sample string) {
	if len(sample) == 0 {
		return
	}

	// add end of text token
	tokens := append(m.tokenizer.Encode(sample), 0)

	for n := range m.N + 1 {
		for _, ngram := range ngrams(tokens, n) {
			m.Counts[m.tokenizer.Decode(ngram)]++
		}
	}
}

func (m *NgramModel) countOf(ctx []uint8) uint64 {
	var count uint64

	for {
		if len(ctx) == 0 {
			count = 0
			break
		}

		count = m.Counts[m.tokenizer.Decode(ctx)]

		if count == 0 {
			ctx = ctx[1:]
		} else {
			break
		}
	}

	return count
}

func (m *NgramModel) probs(text string) []float64 {
	var probs []float64
	total := float64(0)

	var vocabSize = m.tokenizer.VocabSize()

	context := m.tokenizer.Encode(text)
	context = context[len(context)-m.N+1:]

	var continuation = func(tok uint8) []uint8 {
		out := make([]uint8, len(context))
		copy(out, context)
		return append(out, tok)
	}

	for i := range vocabSize {
		total += float64(m.countOf(continuation(uint8(i))))
	}

	total += float64(vocabSize) * m.Smoothing

	for i := range vocabSize {
		if total > 0 {
			var count = float64(m.countOf(continuation(uint8(i)))) + m.Smoothing
			probs = append(probs, count/total)
		} else {
			probs = append(probs, 0.0)
		}
	}

	return probs
}

func sample(probs []float64) uint32 {
	if len(probs) == 0 {
		return 0
	}

	var total float64
	for _, prob := range probs {
		total += prob
	}

	r := rand.Float64() * total
	for i, prob := range probs {
		if r < prob {
			return uint32(i)
		}
		r -= prob
	}

	return 0
}

func (m *NgramModel) generate(seed string, length int) string {
	if len(seed) < 2 {
		return ""
	}

	var out = seed

	for i := 0; i < length; i++ {
		sampled := sample(m.probs(out))

		var next = m.tokenizer.Decode([]uint8{uint8(sampled)})
		out += next

		if sampled == 0 {
			break
		}
	}

	return out
}
