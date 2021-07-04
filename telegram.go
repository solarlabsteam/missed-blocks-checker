package main

import (
	"fmt"
	"strings"
	"time"

	tb "gopkg.in/tucnak/telebot.v2"
)

type TelegramReporter struct {
	TelegramToken      string
	TelegramChat       int
	TelegramConfigPath string

	TelegramBot *tb.Bot
}

func (r TelegramReporter) Serialize(report Report) string {
	var sb strings.Builder

	for _, entry := range report.Entries {
		var (
			emoji         string
			status        string
			validatorLink string
			timeToJail    string = ""
		)

		switch entry.Direction {
		case START_MISSING_BLOCKS:
			emoji = "🚨"
			status = "is missing blocks"
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		case MISSING_BLOCKS:
			emoji = "🔴"
			status = "is missing blocks"
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		case STOPPED_MISSING_BLOCKS:
			emoji = "🟡"
			status = "stopped missing blocks"
		case WENT_BACK_TO_NORMAL:
			emoji = "🟢"
			status = "went back to normal"
		case JAILED:
			emoji = "❌"
			status = "was jailed"
		}

		if entry.ValidatorAddress != "" && entry.ValidatorMoniker != "" {
			validatorLink = fmt.Sprintf(
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorMoniker,
			)
		} else if entry.ValidatorMoniker == "" { // validator with empty moniker, can happen
			validatorLink = fmt.Sprintf(
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				MintscanPrefix,
				entry.ValidatorAddress,
				entry.ValidatorAddress,
			)
		} else {
			validatorLink = fmt.Sprintf("<code>%s</code>", entry.Pubkey)
		}

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>: %d -> %d%s\n",
			emoji,
			validatorLink,
			status,
			entry.BeforeBlocksMissing,
			entry.NowBlocksMissing,
			timeToJail,
		))
	}

	return sb.String()
}

func (r *TelegramReporter) Init() {
	if r.TelegramToken == "" || r.TelegramChat == 0 || r.TelegramConfigPath == "" {
		log.Debug().Msg("Telegram credentials or config path not set, not creating Telegram reporter.")
		return
	}

	bot, err := tb.NewBot(tb.Settings{
		Token:  TelegramToken,
		Poller: &tb.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Warn().Err(err).Msg("Could not create Telegram bot")
		return
	}

	bot.Handle("/start", r.getHelp)
	bot.Handle("/help", r.getHelp)

	r.TelegramBot = bot
}

func (r TelegramReporter) Enabled() bool {
	return r.TelegramBot != nil
}

func (r TelegramReporter) SendReport(report Report) error {
	serializedReport := r.Serialize(report)
	_, err := r.TelegramBot.Send(
		&tb.User{
			ID: r.TelegramChat,
		},
		serializedReport,
		tb.ModeHTML,
	)
	return err
}

func (r TelegramReporter) Name() string {
	return "TelegramReporter"
}

func (r TelegramReporter) sendMessage(message *tb.Message, text string) {
	_, err := r.TelegramBot.Send(
		message.Chat,
		text,
		&tb.SendOptions{
			ParseMode: tb.ModeHTML,
			ReplyTo:   message,
		},
	)

	if err != nil {
		log.Error().Err(err).Msg("Could not send Telegram message")
	}
}

func (r TelegramReporter) getHelp(message *tb.Message) {
	var sb strings.Builder
	sb.WriteString("<strong>missed-block-checker</strong>\n\n")
	sb.WriteString(fmt.Sprintf("Query for the %s network info.\n", MintscanPrefix))
	sb.WriteString("Can understand the following commands:\n")
	sb.WriteString("- /subscribe &lt;validator address&gt; - be notified on validator's missed block in a Telegram channel)\n")
	sb.WriteString("- /unsubscribe &lt;validator address&gt; - undo the subscription given at the previous step\n")
	sb.WriteString("- /status &lt;validator address&gt; - get validator missed blocks\n\n")
	sb.WriteString("- /status - get the missed blocks of the validator(s) you're subscribed to\n\n")
	sb.WriteString("Created by <a href=\"https://validator.solar\">SOLAR Labs</a> with ❤️.\n")
	sb.WriteString("This bot is open-sourced, you can get the source code at https://github.com/solarlabsteam/missed-blocks-checker.\n\n")
	sb.WriteString("We also maintain the following tools for Cosmos ecosystem:\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-interacter\">cosmos-interacter</a> - a bot that can return info about Cosmos-based blockchain params.\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-exporter\">cosmos-exporter</a> - scrape the blockchain data from the local node and export it to Prometheus\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/coingecko-exporter\">coingecko-exporter</a> - scrape the Coingecko exchange rate and export it to Prometheus\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-transactions-bot\">cosmos-transactions-bot</a> - monitor the incoming transactions for a given filter\n\n")
	sb.WriteString("If you like what we're doing, consider staking with us!\n")
	sb.WriteString("- <a href=\"https://www.mintscan.io/sentinel/validators/sentvaloper1sazxkmhym0zcg9tmzvc4qxesqegs3q4u66tpmf\">Sentinel</a>\n")
	sb.WriteString("- <a href=\"https://www.mintscan.io/persistence/validators/persistencevaloper1kp2sype5n0ky3f8u50pe0jlfcgwva9y79qlpgy\">Persistence</a>\n")
	sb.WriteString("- <a href=\"https://www.mintscan.io/osmosis/validators/osmovaloper16jn3383fn4v4vuuvgclr3q7rumeglw8kdq6e48\">Osmosis</a>\n")

	r.sendMessage(message, sb.String())
	log.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned help info")
}
