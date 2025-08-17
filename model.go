package main

import (
	"math/rand/v2"
	"strings"
)

type Token int

type Tokenizer interface {
	Encode(text string) []Token
	Decode(tokens []Token) string
	Observe(text string)
	VocabSize() int
}

type CharTokenizer struct {
	vocab          []rune
	special_tokens []string // special tokens need strings to be displayed (e.g. <|endoftext|>)
}

func NewCharTokenizer(special_tokens []string) *CharTokenizer {
	if len(special_tokens) == 0 {
		special_tokens = []string{
			"<|endoftext|>",
		}
	}

	var c = &CharTokenizer{
		vocab:          make([]rune, 0),
		special_tokens: special_tokens,
	}

	return c
}

func (c *CharTokenizer) Encode(text string) []Token {
	var tokens []Token

	for _, r := range text {
		tok := strings.IndexRune(string(c.vocab), r)

		// use -1 for unknown tokens and adjust the tok id for known tokens
		if tok >= 0 {
			tok += len(c.special_tokens)
		}

		tokens = append(tokens, Token(tok))
	}

	return tokens
}

func (c *CharTokenizer) Decode(tokens []Token) string {
	var sb strings.Builder

	for _, tok := range tokens {
		if tok < 0 || int(tok) >= c.VocabSize() {
			sb.WriteRune('�') // unknown token
			continue
		}

		if len(c.special_tokens) <= int(tok) {
			// adjust the token id to match the vocab index
			sb.WriteRune(c.vocab[int(tok)-len(c.special_tokens)])
		} else {
			sb.WriteString(c.special_tokens[tok])
		}
	}

	return sb.String()
}

func (c *CharTokenizer) Observe(text string) {
	for _, r := range text {
		if !strings.ContainsRune(string(c.vocab), r) {
			c.vocab = append(c.vocab, r)
		}
	}
}

func (c *CharTokenizer) VocabSize() int {
	return len(c.special_tokens) + len(c.vocab)
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
	m.tokenizer.Observe(sample)

	// add end of text token
	tokens := append(m.tokenizer.Encode(sample), 0)

	for n := range m.N + 1 {
		for _, ngram := range ngrams(tokens, n) {
			m.Counts[m.tokenizer.Decode(ngram)]++
		}
	}
}

func (m *NgramModel) countOf(ctx []Token, nMin int) uint64 {
	var count uint64 = 0

	for {
		if len(ctx) == 0 {
			count = 0
			break
		}

		count = m.Counts[m.tokenizer.Decode(ctx)]

		if count == 0 && len(ctx) > nMin {
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

	var continuation = func(tok Token) []Token {
		out := make([]Token, len(context))
		copy(out, context)
		return append(out, tok)
	}

	total = float64(m.countOf(context, m.N)) + float64(vocabSize)*m.Smoothing

	for i := range vocabSize {
		if total > 0 {
			var count = float64(m.countOf(continuation(Token(i)), m.N)) + m.Smoothing
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
	if len(seed) < m.N-1 {
		return ""
	}

	var out = seed

	for range length {
		sampled := sample(m.probs(out))

		var next = m.tokenizer.Decode([]Token{Token(sampled)})

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

	tokens := m.tokenizer.Encode(text)
	tokens = append(tokens, 0) // add end of text token

	for n := range m.N + 1 {
		for _, ngram := range ngrams(tokens, n) {
			key := m.tokenizer.Decode(ngram)
			if count, exists := m.Counts[key]; exists {
				if count > 0 {
					m.Counts[key]--
				}
			}
		}
	}
}
