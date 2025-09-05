package main

import (
	"math/rand/v2"
	"strings"
)

type Token int

type Tokenizer struct {
	Vocab         []rune
	SpecialTokens []string // special tokens need strings to be displayed (e.g. <|endoftext|>)
}

func makeCharTokenizer(special_tokens []string) Tokenizer {
	if len(special_tokens) == 0 {
		special_tokens = []string{
			"<|endoftext|>",
		}
	}

	return Tokenizer{
		Vocab:         make([]rune, 0),
		SpecialTokens: special_tokens,
	}
}

func (c *Tokenizer) Encode(text string) []Token {
	var tokens []Token

	for _, r := range text {
		tok := strings.IndexRune(string(c.Vocab), r)

		// use -1 for unknown tokens and adjust the tok id for known tokens
		if tok >= 0 {
			tok += len(c.SpecialTokens)
		}

		tokens = append(tokens, Token(tok))
	}

	return tokens
}

func (c *Tokenizer) Decode(tokens []Token) string {
	var sb strings.Builder

	for _, tok := range tokens {
		if tok < 0 || int(tok) >= c.VocabSize() {
			sb.WriteRune('�') // unknown token
			continue
		}

		if len(c.SpecialTokens) <= int(tok) {
			// adjust the token id to match the vocab index
			sb.WriteRune(c.Vocab[int(tok)-len(c.SpecialTokens)])
		} else {
			sb.WriteString(c.SpecialTokens[tok])
		}
	}

	return sb.String()
}

func (c *Tokenizer) Observe(text string) {
	for _, r := range text {
		if !strings.ContainsRune(string(c.Vocab), r) {
			c.Vocab = append(c.Vocab, r)
		}
	}
}

func (c *Tokenizer) VocabSize() int {
	return len(c.SpecialTokens) + len(c.Vocab)
}

type NgramModel struct {
	Counts map[string]uint64

	Tokenizer Tokenizer
	N         int
	Smoothing float64

	Total int
}

func NewNgramModel(tokenizer Tokenizer, n int, smoothing float64) *NgramModel {
	model := &NgramModel{
		Counts:    make(map[string]uint64),
		Tokenizer: tokenizer,
		N:         n,
		Smoothing: smoothing,
	}

	return model
}

func ngrams(tokens []Token, n int) [][]Token {
	var ngrams [][]Token

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

	// update the tokenizer vocab
	m.Tokenizer.Observe(sample)

	// add end of text token
	tokens := append(m.Tokenizer.Encode(sample), 0)

	for n := range m.N + 1 {
		for _, ngram := range ngrams(tokens, n) {
			m.Counts[m.Tokenizer.Decode(ngram)]++
			m.Total++
		}
	}
}

func (m *NgramModel) countOf(ctx []Token) uint64 {
	return m.Counts[m.Tokenizer.Decode(ctx)]
}

func (m *NgramModel) probs(text string) []float64 {
	var probs []float64
	total := float64(0)

	var vocabSize = m.Tokenizer.VocabSize()

	context := m.Tokenizer.Encode(text)
	if len(context) >= m.N-1 {
		context = context[len(context)-m.N+1:]
	}

	var continuation = func(tok Token) []Token {
		out := make([]Token, len(context))
		copy(out, context)
		return append(out, tok)
	}

	if len(context) > 0 {
		total = float64(m.countOf(context)) + float64(vocabSize)*m.Smoothing
	} else {
		total = float64(m.Total)
	}

	for i := range vocabSize {
		if total > 0 {
			var count = float64(m.countOf(continuation(Token(i)))) + m.Smoothing
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
	var out = seed

	for range length {
		sampled := sample(m.probs(out))

		var next = m.Tokenizer.Decode([]Token{Token(sampled)})

		if sampled == 0 {
			break
		}

		out += next
	}

	return out
}

func (m *NgramModel) forget(text string) {
	if len(text) == 0 {
		return
	}

	tokens := m.Tokenizer.Encode(text)
	tokens = append(tokens, 0) // add end of text token

	for n := range m.N + 1 {
		for _, ngram := range ngrams(tokens, n) {
			key := m.Tokenizer.Decode(ngram)
			if count, exists := m.Counts[key]; exists {
				if count > 0 {
					m.Counts[key]--
				}
			}
		}
	}
}
