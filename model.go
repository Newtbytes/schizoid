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
	counts map[string]uint64

	tokenizer Tokenizer
	n         int
}

func NewNgramModel(tokenizer Tokenizer, n int) *NgramModel {
	model := &NgramModel{
		counts:    make(map[string]uint64),
		tokenizer: tokenizer,
		n:         n,
	}

	return model
}

func ngrams(tokens []uint8, n int) [][]uint8 {
	var ngrams [][]uint8

	for i := 0; i < len(tokens)-n; i++ {
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
	for _, ngram := range ngrams(tokens, m.n) {
		m.counts[m.tokenizer.Decode(ngram)]++
	}
}

func (m *NgramModel) probs(text string) []float64 {
	var probs []float64
	total := uint64(0)

	// context is a single character as this is a bigram model
	context := m.tokenizer.Encode(text)[len(text)-m.n+1:]

	for i := 0; i < len(m.counts); i++ {
		var query = append(context, uint8(i))
		total += m.counts[m.tokenizer.Decode(query)]
	}

	for i := 0; i < len(m.counts); i++ {
		if total > 0 {
			var query = append(context, uint8(i))
			probs = append(probs, float64(m.counts[m.tokenizer.Decode(query)])/float64(total))
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
		if sampled == 0 {
			break
		}

		var next = m.tokenizer.Decode([]uint8{uint8(sampled)})
		out += next
	}

	return out
}
