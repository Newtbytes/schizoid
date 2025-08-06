package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

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
)

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
	var message string
	if event.Message.Content == "?schizoid" {
		message = "schizoid has arisen from their slumber"
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
