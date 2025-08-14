package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

type TrainedSpan struct {
	start time.Time
	end   time.Time

	startID snowflake.ID
	endID   snowflake.ID
}

func (ts *TrainedSpan) DuringSpan(t time.Time) bool {
	return (t.After(ts.start) && t.Before(ts.end)) || t.Equal(ts.start) || t.Equal(ts.end)
}

func (ts *TrainedSpan) ExtendSpan(msg discord.Message) {
	var t = msg.CreatedAt

	if t.After(ts.end) {
		ts.end = t
		ts.endID = msg.ID
	}

	if t.Before(ts.start) {
		ts.start = t
		ts.startID = msg.ID
	}
}

func (ts *TrainedSpan) Union(other *TrainedSpan) {
	if other.start.Before(ts.start) {
		ts.start = other.start
		ts.startID = other.startID
	}
	if other.end.After(ts.end) {
		ts.end = other.end
		ts.endID = other.endID
	}
}

func makeSpan(msg discord.Message) *TrainedSpan {
	return &TrainedSpan{
		start: msg.CreatedAt,
		end:   msg.CreatedAt,

		startID: msg.ID,
		endID:   msg.ID,
	}
}

type Brain struct {
	model        *NgramModel
	trainedSpans map[snowflake.ID]*TrainedSpan

	mu sync.RWMutex
}

func NewBrain() *Brain {
	b := &Brain{
		model:        NewNgramModel(&CharTokenizer{}, 5, 1),
		trainedSpans: make(map[snowflake.ID]*TrainedSpan),
	}

	return b
}

func (b *Brain) getTrainedSpan(channelID snowflake.ID) *TrainedSpan {
	b.mu.RLock()

	ts := b.trainedSpans[channelID]
	if ts == nil {
		b.mu.RUnlock()
		return nil
	}

	b.mu.RUnlock()

	return ts
}

func (b *Brain) setTrainedSpan(channelID snowflake.ID, span *TrainedSpan) {
	b.mu.Lock()

	b.trainedSpans[channelID] = span

	b.mu.Unlock()
}

func (b *Brain) observe(obs discord.Message) {
	var span = b.getTrainedSpan(obs.ChannelID)

	if span != nil {
		if span.DuringSpan(obs.CreatedAt) {
			return
		}
	}

	b.mu.Lock()
	b.model.train(obs.Content)
	b.mu.Unlock()

	if span == nil {
		b.setTrainedSpan(obs.ChannelID, makeSpan(obs))
	} else {
		span.ExtendSpan(obs)
		b.setTrainedSpan(obs.ChannelID, span)
	}
}

func (b *Brain) observeSomeMessages(client bot.Client, channelID snowflake.ID) {
	var span = b.getTrainedSpan(channelID)

	if span == nil {
		return
	}

	var msgID = span.startID

	slog.Info("Observing messages in channel", slog.String("channelID", channelID.String()), slog.Time("start", span.start))

	var messages, err = client.Rest().GetMessages(channelID, msgID, msgID, msgID, 25)

	if err != nil {
		return
	}

	for _, msg := range messages {
		b.observe(msg)
	}

	slog.Info("Trained:", slog.String("channelID", channelID.String()), slog.Time("start", b.trainedSpans[channelID].start), slog.Time("end", b.trainedSpans[channelID].end))
}

func (b *Brain) generate(seed string, length int) string {
	b.mu.Lock()
	var out = b.model.generate(seed, length)
	b.mu.Unlock()

	return out
}
