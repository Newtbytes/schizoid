package main

import (
	"context"
	"log"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"github.com/joho/godotenv"
)

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

var (
	token         = os.Getenv("DISCORD_TOKEN")
	guildID       = snowflake.GetEnv("GUILD_ID")
	trainInterval = os.Getenv("TRAIN_INTERVAL_SECONDS")

	commands = []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "echo",
			Description: "repeats what you say back to you",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{
					Name:        "message",
					Description: "What to say",
					Required:    true,
				},
			},
		},
	}

	guilds = make(map[snowflake.ID]*Brain)
)

func retrieve_guild_brain(id snowflake.ID) *Brain {
	if guilds[id] == nil {
		guilds[id] = NewBrain()
		guilds[id].model = NewNgramModel(&CharTokenizer{}, 5)
	}

	return guilds[id]
}

func main() {
	err := godotenv.Load()
	if err != nil {
		slog.Error("Failed to load environment", slog.Any("err", err))
	}

	token = os.Getenv("DISCORD_TOKEN")
	guildID = snowflake.GetEnv("GUILD_ID")

	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuildMessages,
				gateway.IntentMessageContent,
				gateway.IntentGuildScheduledEvents,
			),
			gateway.WithRateLimiter(gateway.NewRateLimiter()),
		),
		bot.WithEventListenerFunc(onMessageCreate),
		bot.WithEventListenerFunc(commandListener),
	)

	if err != nil {
		slog.Error("Failed to create client", slog.Any("err", err))
		return
	}

	defer client.Close(context.TODO())

	if _, err = client.Rest().SetGuildCommands(client.ApplicationID(), guildID, commands); err != nil {
		slog.Error("Failed to setup commands", slog.Any("err", err))
		return
	}

	if err = client.OpenGateway(context.TODO()); err != nil {
		slog.Error("Failed to open gateway", slog.Any("err", err))
		return
	}

	log.Print("schizoid is now running. Press CTRL-C to exit.")

	go observeChannels(client, guildID)

	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-s
}

func observeChannels(client bot.Client, guildID snowflake.ID) {
	brain := retrieve_guild_brain(guildID)

	trainInterval = os.Getenv("TRAIN_INTERVAL_SECONDS")
	if trainInterval == "" {
		trainInterval = "60"
	}

	var interval, err = time.ParseDuration(trainInterval + "s")
	if err != nil {
		slog.Error("Failed to parse TRAIN_INTERVAL_SECONDS", slog.Any("err", err))
		interval = 60 * time.Second
	}

	for {
		if len(brain.trainedSpans) == 0 {
			continue
		}

		for channelID := range brain.trainedSpans {
			go brain.observeSomeMessages(client, channelID)
		}

		time.Sleep(interval)
	}
}

func onMessageCreate(event *events.MessageCreate) {
	if event.Message.Author.Bot {
		return
	}

	var schizo = retrieve_guild_brain(*event.GuildID)
	schizo.observe(event.Message)

	var message string
	if strings.HasPrefix(event.Message.Content, "?schizoid") {
		var seed = event.Message.Content[len("?schizoid "):]
		message = schizo.model.generate(seed, 100)
	}

	if message != "" {
		_, _ = event.Client().Rest().CreateMessage(event.ChannelID, discord.NewMessageCreateBuilder().SetContent(message).Build())
	}
}

func commandListener(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.CommandName() == "echo" {
		err := event.CreateMessage(discord.NewMessageCreateBuilder().
			SetContent(data.String("message")).
			Build(),
		)
		if err != nil {
			log.Fatal(err)
		}
	}
}
