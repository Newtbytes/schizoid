package main

import (
	"log/slog"
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
}

func NewBrain() *Brain {
	b := &Brain{
		model:        NewNgramModel(&CharTokenizer{}, 5),
		trainedSpans: make(map[snowflake.ID]*TrainedSpan),
	}

	return b
}

func (b *Brain) observe(obs discord.Message) {
	var span = b.trainedSpans[obs.ChannelID]

	if b.trainedSpans[obs.ChannelID] != nil {
		if span.DuringSpan(obs.CreatedAt) {
			return
		}
	}

	b.model.train(obs.Content)

	if b.trainedSpans[obs.ChannelID] == nil {
		b.trainedSpans[obs.ChannelID] = makeSpan(obs)
	} else {
		b.trainedSpans[obs.ChannelID].ExtendSpan(obs)
	}

}

func (b *Brain) observeSomeMessages(client bot.Client, channelID snowflake.ID) {
	if b.trainedSpans[channelID] == nil {
		return
	}

	var msgID = b.trainedSpans[channelID].startID

	slog.Info("Observing messages in channel", slog.String("channelID", channelID.String()), slog.Time("start", b.trainedSpans[channelID].start))

	var messages, err = client.Rest().GetMessages(channelID, msgID, msgID, msgID, 25)

	if err != nil {
		return
	}

	for _, msg := range messages {
		b.observe(msg)
	}

	slog.Info("Trained:", slog.String("channelID", channelID.String()), slog.Time("start", b.trainedSpans[channelID].start), slog.Time("end", b.trainedSpans[channelID].end))
}
