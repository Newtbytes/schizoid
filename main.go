package main

import (
	"context"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
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
	// actually just a bigram model TwT
	counts [][]uint64

	tokenizer Tokenizer
}

func NewNgramModel(tokenizer Tokenizer) *NgramModel {
	model := &NgramModel{
		tokenizer: tokenizer,
	}

	// Initialize counts
	vocabSize := tokenizer.VocabSize()
	model.counts = make([][]uint64, vocabSize)
	for i := range model.counts {
		model.counts[i] = make([]uint64, vocabSize)
	}

	return model
}

func bigrams(text []uint8) [][]uint8 {
	var bigrams [][]uint8

	for i := 0; i < len(text)-1; i++ {
		bigrams = append(bigrams, []uint8{text[i], text[i+1]})
	}

	return bigrams
}

func (m *NgramModel) train(sample string) {
	if len(sample) == 0 {
		return
	}

	// add end of text token
	tokens := append(m.tokenizer.Encode(sample), 0)
	for _, bigram := range bigrams(tokens) {
		m.counts[bigram[0]][bigram[1]]++
	}
}

func (m *NgramModel) probs(text string) []float64 {
	var probs []float64
	total := uint64(0)

	// context is a single character as this is a bigram model
	context := m.tokenizer.Encode(text)[len(text)-1]

	for i := 0; i < len(m.counts); i++ {
		total += m.counts[context][i]
	}

	for i := 0; i < len(m.counts); i++ {
		if total > 0 {
			probs = append(probs, float64(m.counts[context][i])/float64(total))
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

type TimeSpan struct {
	start time.Time
	end   time.Time
}

func (ts *TimeSpan) DuringSpan(t time.Time) bool {
	return t.After(ts.start) && t.Before(ts.end)
}

func (ts *TimeSpan) ExtendSpan(t time.Time) {
	if t.After(ts.end) {
		ts.end = t
	}
}

func (ts *TimeSpan) Union(other *TimeSpan) {
	if other.start.Before(ts.start) {
		ts.start = other.start
	}
	if other.end.After(ts.end) {
		ts.end = other.end
	}
}

func makeSpan(timestamp time.Time) *TimeSpan {
	return &TimeSpan{
		start: timestamp,
		end:   timestamp,
	}
}

type Brain struct {
	model        *NgramModel
	trainedSpans map[snowflake.ID]*TimeSpan
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
		b.trainedSpans[obs.ChannelID] = makeSpan(obs.CreatedAt)
	} else {
		b.trainedSpans[obs.ChannelID].ExtendSpan(obs.CreatedAt)
	}
}

var (
	token   = os.Getenv("DISCORD_TOKEN")
	guildID = snowflake.GetEnv("GUILD_ID")

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
		guilds[id] = new(Brain)
		guilds[id].model = NewNgramModel(&CharTokenizer{})
	}

	return guilds[id]
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Failed to load environment: %s", err)
	}

	token = os.Getenv("DISCORD_TOKEN")
	guildID = snowflake.GetEnv("GUILD_ID")

	client, err := disgo.New(token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuildMessages,
				gateway.IntentMessageContent,
			),
		),
		bot.WithEventListenerFunc(onMessageCreate),
		bot.WithEventListenerFunc(commandListener),
	)

	if err != nil {
		log.Fatalf("Failed to create client: %s", err)
	}

	defer client.Close(context.TODO())

	if _, err = client.Rest().SetGuildCommands(client.ApplicationID(), guildID, commands); err != nil {
		log.Fatalf("Failed to setup commands: %s", err)
		return
	}

	if err = client.OpenGateway(context.TODO()); err != nil {
		log.Fatalf("Failed to open gateway: %s", err)
		return
	}

	log.Print("schizoid is now running. Press CTRL-C to exit.")
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-s
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
