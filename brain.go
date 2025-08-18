package main

import (
	"bytes"
	"encoding/gob"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

type TrainedSpan struct {
	Start time.Time
	End   time.Time

	StartID snowflake.ID
	EndID   snowflake.ID
}

func (ts *TrainedSpan) DuringSpan(t time.Time) bool {
	return (t.After(ts.Start) && t.Before(ts.End)) || t.Equal(ts.Start) || t.Equal(ts.End)
}

func (ts *TrainedSpan) ExtendSpan(msg discord.Message) {
	var t = msg.CreatedAt

	if t.After(ts.End) {
		ts.End = t
		ts.EndID = msg.ID
	}

	if t.Before(ts.Start) {
		ts.Start = t
		ts.StartID = msg.ID
	}
}

func (ts *TrainedSpan) Union(other *TrainedSpan) {
	if other.Start.Before(ts.Start) {
		ts.Start = other.Start
		ts.StartID = other.StartID
	}
	if other.End.After(ts.End) {
		ts.End = other.End
		ts.EndID = other.EndID
	}
}

func makeSpan(msg discord.Message) *TrainedSpan {
	return &TrainedSpan{
		Start: msg.CreatedAt,
		End:   msg.CreatedAt,

		StartID: msg.ID,
		EndID:   msg.ID,
	}
}

type Brain struct {
	Model        *NgramModel
	TrainedSpans map[snowflake.ID]*TrainedSpan
	GuildID      snowflake.ID

	mu sync.RWMutex
}

func NewBrain(guildID snowflake.ID) *Brain {
	b := &Brain{
		Model:        NewNgramModel(NewCharTokenizer([]string{}), 5, 0),
		TrainedSpans: make(map[snowflake.ID]*TrainedSpan),
		GuildID:      guildID,
	}

	return b
}

func (b *Brain) getTrainedSpan(channelID snowflake.ID) *TrainedSpan {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.TrainedSpans[channelID]
}

func (b *Brain) setTrainedSpan(channelID snowflake.ID, span *TrainedSpan) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.TrainedSpans[channelID] = span
}

func (b *Brain) Save() {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)

	err := encoder.Encode(b)
	if err != nil {
		slog.Error("Error serializing:", slog.Any("err", err))
		return
	}

	if err := os.MkdirAll("models", 0755); err != nil {
		slog.Error("Failed to create models directory", slog.Any("err", err))
		return
	}

	fn := "models/" + b.GuildID.String() + ".brain"
	os.WriteFile(fn, buffer.Bytes(), 0644)

	slog.Info("Serialized guild brain with ID", slog.Any("guildID", b.GuildID))
}

func LoadBrain(guildID snowflake.ID) *Brain {
	var buffer bytes.Buffer
	fn := "models/" + guildID.String() + ".brain"

	if _, err := os.Stat(fn); os.IsNotExist(err) {
		slog.Info("Brain file does not exist, creating new brain", slog.Any("guildID", guildID))
		return NewBrain(guildID)
	}

	data, err := os.ReadFile(fn)
	if err != nil {
		slog.Error("Failed to read brain file", slog.String("file", fn), slog.Any("error", err))
		return NewBrain(guildID)
	}

	buffer.Write(data)

	var brain Brain
	decoder := gob.NewDecoder(&buffer)
	err = decoder.Decode(&brain)
	if err != nil {
		slog.Error("Failed to decode brain data", slog.Any("error", err))
		return NewBrain(guildID)
	}

	brain.Model.Tokenizer = &CharTokenizer{}

	slog.Info("Loaded brain for guild", slog.Any("guildID", guildID), slog.Int("trainedSpans", len(brain.TrainedSpans)))
	return &brain
}

func (b *Brain) shouldObserve(obs discord.Message) bool {
	if obs.Author.Bot {
		return false
	}

	if len(obs.Content) == 0 {
		return false
	}

	return true
}

func (b *Brain) observe(obs discord.Message) {
	var span = b.getTrainedSpan(obs.ChannelID)

	if span != nil {
		if span.DuringSpan(obs.CreatedAt) {
			return
		}
	}

	if b.shouldObserve(obs) {
		b.mu.Lock()
		b.Model.train(obs.Content)
		b.mu.Unlock()
	}

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

	var msgID = span.StartID

	var messages, err = client.Rest().GetMessages(channelID, msgID, msgID, msgID, 25)

	if err != nil {
		return
	}

	for _, msg := range messages {
		b.observe(msg)
	}

	slog.Info("Trained:", slog.String("channelID", channelID.String()), slog.Time("start", b.TrainedSpans[channelID].Start), slog.Time("end", b.TrainedSpans[channelID].End))
}

func (b *Brain) generate(seed string, length int) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.Model.generate(seed, length)
}

func (b *Brain) forget(obs discord.Message) {
	if len(obs.Content) == 0 {
		return
	}

	if !b.shouldObserve(obs) {
		return
	}

	span := b.getTrainedSpan(obs.ChannelID)
	if span == nil {
		return
	}

	// avoid forgetting messages that have not been observed
	if !span.DuringSpan(obs.CreatedAt) {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.Model.forget(obs.Content)
}
