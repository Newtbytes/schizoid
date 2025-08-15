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
	trainInterval = os.Getenv("TRAIN_INTERVAL_SECONDS")

	guilds = make(map[snowflake.ID]*Brain)
)

func retrieve_guild_brain(client bot.Client, id snowflake.ID) *Brain {
	if guilds[id] == nil {
		guilds[id] = LoadBrain(id)
		go observeChannels(client, id)
	}

	return guilds[id]
}

func main() {
	err := godotenv.Load()
	if err != nil {
		slog.Error("Failed to load environment", slog.Any("err", err))
	}

	token = os.Getenv("DISCORD_TOKEN")

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
	)

	if err != nil {
		slog.Error("Failed to create client", slog.Any("err", err))
		return
	}

	defer client.Close(context.TODO())
	defer func() {
		for _, brain := range guilds {
			brain.Save()
		}
	}()

	if err = client.OpenGateway(context.TODO()); err != nil {
		slog.Error("Failed to open gateway", slog.Any("err", err))
		return
	}

	log.Print("schizoid is now running. Press CTRL-C to exit.")

	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-s
}

func observeChannels(client bot.Client, guildID snowflake.ID) {
	brain := retrieve_guild_brain(client, guildID)

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
		if len(brain.TrainedSpans) == 0 {
			time.Sleep(time.Second)
			continue
		}

		for channelID := range brain.TrainedSpans {
			go brain.observeSomeMessages(client, channelID)
		}

		time.Sleep(interval)
	}
}

func onMessageCreate(event *events.MessageCreate) {
	if event.Message.Author.Bot {
		return
	}

	var schizo = retrieve_guild_brain(event.Client(), *event.GuildID)
	schizo.observe(event.Message)

	var message string
	if strings.HasPrefix(event.Message.Content, "?schizoid") {
		var seed = event.Message.Content[len("?schizoid "):]
		message = schizo.generate(seed, 100)
	}

	if message != "" {
		_, _ = event.Client().Rest().CreateMessage(event.ChannelID, discord.NewMessageCreateBuilder().SetContent(message).Build())
	}
}
