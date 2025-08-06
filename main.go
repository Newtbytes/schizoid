package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
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

type Observation struct {
	content string
	author  string
	time    time.Time
}

func (o *Observation) String() {
	if o == nil {
		return
	}
	fmt.Printf("Observation: %s\nAuthor: %s\nTime: %s\n", o.content, o.author, o.time.Format(time.RFC3339))
}

func make_observation(msg discord.Message) Observation {
	return Observation{msg.Content, msg.Author.Username, msg.CreatedAt}
}

type Brain struct {
	memory []Observation
}

func (b *Brain) observe(obs discord.Message) {
	b.memory = append(b.memory, make_observation(obs))
}

func (b *Brain) String() string {
	if b == nil {
		return "No observations yet."
	}

	var result string
	for _, obs := range b.memory {
		result += fmt.Sprintf("Observation: %s\nAuthor: %s\nTime: %s\n", obs.content, obs.author, obs.time.Format(time.RFC3339))
	}
	return result
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
	if event.Message.Content == "?schizoid" {
		message = fmt.Sprint(schizo)
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
