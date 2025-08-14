package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
	"github.com/joho/godotenv"
)

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
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagsAll),
		),

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

	if message == event.Message.Content {
		message = "Failed to generate message"
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
