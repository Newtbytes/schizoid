package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"
	"github.com/joho/godotenv"
)

var (
	token         = os.Getenv("DISCORD_TOKEN")
	trainInterval = os.Getenv("TRAIN_INTERVAL_SECONDS")

	guilds = make(map[snowflake.ID]*Brain)

	commands = []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "watchchannel",
			Description: "let schizoid learn from a channel",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionChannel{
					Name:        "channel",
					Description: "Channel to learn from",
					Required:    true,
				},
			},
		},
	}
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
		slog.Error("Failed to load environment", slog.String("err", err.Error()))
	}

	token = os.Getenv("DISCORD_TOKEN")

	r := handler.New()

	r.SlashCommand("/watchchannel", handleWatchChannel)

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
		bot.WithEventListenerFunc(onMessageDelete),
		bot.WithEventListeners(r),
	)

	if err != nil {
		slog.Error("Failed to create client", slog.String("err", err.Error()))
		return
	}

	defer client.Close(context.TODO())
	defer func() {
		for _, brain := range guilds {
			brain.Save()
		}
	}()

	if err = client.OpenGateway(context.TODO()); err != nil {
		slog.Error("Failed to open gateway", slog.String("err", err.Error()))
		panic(err)
	}

	if _, err = client.Rest().SetGlobalCommands(client.ApplicationID(), commands); err != nil {
		slog.Error("Failed to register commands", slog.String("err", err.Error()))
		panic(err)
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
		slog.Error("Failed to parse TRAIN_INTERVAL_SECONDS", slog.String("err", err.Error()))
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

	// respond if bot is mentioned
	mentioned_users := event.Message.Mentions
	if slices.ContainsFunc(mentioned_users, func(u discord.User) bool { return u.ID == event.Client().ID() }) {
		message = schizo.generate(event.Message.Content, 512)
	}

	if message != "" {
		_, _ = event.Client().Rest().CreateMessage(event.ChannelID, discord.NewMessageCreateBuilder().SetContent(message).Build())
	}
}

func onMessageDelete(event *events.MessageDelete) {
	if event.Message.Author.Bot {
		return
	}

	var schizo = retrieve_guild_brain(event.Client(), *event.GuildID)

	schizo.forget(event.Message)

	slog.Info(
		"Message was deleted and forgotten",
		slog.String("messageID", event.MessageID.String()),
		slog.String("channelID", event.ChannelID.String()),
		slog.String("guildID", event.GuildID.String()),
	)
}

func handleWatchChannel(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	schizo := retrieve_guild_brain(e.Client(), *e.GuildID())
	channel := data.Channel("channel")
	schizo.WhitelistChannel(channel.ID)

	if err := e.CreateMessage(discord.NewMessageCreateBuilder().
		SetContent("Added channel " + channel.Name + " to whitelist.").
		Build(),
	); err != nil {
		e.Client().Logger().Error("error on sending response", slog.Any("err", err))
		return err
	}

	return nil
}
