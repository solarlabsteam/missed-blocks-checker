package main

import (
	"fmt"
	"html"
	"io/ioutil"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	tb "gopkg.in/tucnak/telebot.v2"
)

const MaxMessageSize = 4096

type TelegramReporter struct {
	ChainInfoConfig   ChainInfoConfig
	TelegramAppConfig TelegramAppConfig
	AppConfig         *AppConfig
	Params            *Params
	Client            *TendermintGRPC
	Logger            zerolog.Logger

	TelegramConfig TelegramConfig
	TelegramBot    *tb.Bot
}

type NotificationInfo struct {
	ValidatorAddress string
	Notifiers        []string
}

type TelegramConfig struct {
	NotiticationInfos []*NotificationInfo
}

func NewTelegramReporter(
	chainInfoConfig ChainInfoConfig,
	telegramAppConfig TelegramAppConfig,
	appConfig *AppConfig,
	params *Params,
	client *TendermintGRPC,
	logger *zerolog.Logger,
) *TelegramReporter {
	return &TelegramReporter{
		ChainInfoConfig:   chainInfoConfig,
		TelegramAppConfig: telegramAppConfig,
		AppConfig:         appConfig,
		Params:            params,
		Client:            client,
		Logger:            logger.With().Str("component", "telegram_reporter").Logger(),
	}
}

func (i *NotificationInfo) addNotifier(notifier string) error {
	if stringInSlice(notifier, i.Notifiers) {
		return fmt.Errorf("You are already subscribed to this validator's notifications.") //nolint
	}

	i.Notifiers = append(i.Notifiers, notifier)
	return nil
}

func (i *NotificationInfo) removeNotifier(notifier string) error {
	if !stringInSlice(notifier, i.Notifiers) {
		return fmt.Errorf("You are not subscribed to this validator's notifications.") //nolint
	}

	i.Notifiers = removeFromSlice(i.Notifiers, notifier)
	return nil
}

func (c *TelegramConfig) getNotifiedValidators(notifier string) []string {
	validators := []string{}
	for _, info := range c.NotiticationInfos {
		if stringInSlice(notifier, info.Notifiers) {
			validators = append(validators, info.ValidatorAddress)
		}
	}

	return validators
}

func (c *TelegramConfig) addNotifier(validatorAddress string, notifierToAdd string) error {
	for _, notifier := range c.NotiticationInfos {
		if notifier.ValidatorAddress == validatorAddress {
			return notifier.addNotifier(notifierToAdd)
		}
	}

	newNotificationInfo := NotificationInfo{ValidatorAddress: validatorAddress, Notifiers: []string{notifierToAdd}}
	c.NotiticationInfos = append(c.NotiticationInfos, &newNotificationInfo)
	return nil
}

func (c *TelegramConfig) removeNotifier(validatorAddress string, notifierToAdd string) error {
	for _, notifier := range c.NotiticationInfos {
		if notifier.ValidatorAddress == validatorAddress {
			return notifier.removeNotifier(notifierToAdd)
		}
	}

	return fmt.Errorf("You are not subscribed to this validator's notifications.") //nolint
}

func (c *TelegramConfig) getNotifiersSerialized(address string) string {
	var sb strings.Builder

	for _, validator := range c.NotiticationInfos {
		if validator.ValidatorAddress == address {
			for _, notifier := range validator.Notifiers {
				sb.WriteString("@" + notifier + " ")
			}
		}
	}

	return sb.String()
}

func (r TelegramReporter) Serialize(report Report) string {
	var sb strings.Builder

	for _, entry := range report.Entries {
		var (
			validatorLink string
			timeToJail    = ""
		)

		if entry.Direction == INCREASING {
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail(r.Params))
		}

		validatorLink = r.ChainInfoConfig.GetValidatorPage(entry.ValidatorAddress, entry.ValidatorMoniker)
		notifiers := r.TelegramConfig.getNotifiersSerialized(entry.ValidatorAddress)

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>%s %s\n",
			entry.Emoji,
			validatorLink,
			html.EscapeString(entry.Description),
			timeToJail,
			notifiers,
		))
	}

	return sb.String()
}

func (r *TelegramReporter) Init() {
	if r.TelegramAppConfig.Token == "" || r.TelegramAppConfig.Chat == 0 || r.TelegramAppConfig.ConfigPath == "" {
		r.Logger.Debug().Msg("Telegram credentials or config path not set, not creating Telegram reporter.")
		return
	}

	bot, err := tb.NewBot(tb.Settings{
		Token:  r.TelegramAppConfig.Token,
		Poller: &tb.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		r.Logger.Warn().Err(err).Msg("Could not create Telegram bot")
		return
	}

	r.TelegramBot = bot
	r.TelegramBot.Handle("/start", r.getHelp)
	r.TelegramBot.Handle("/help", r.getHelp)
	r.TelegramBot.Handle("/status", r.getValidatorStatus)
	r.TelegramBot.Handle("/subscribe", r.subscribeToValidatorUpdates)
	r.TelegramBot.Handle("/unsubscribe", r.unsubscribeFromValidatorUpdates)
	r.TelegramBot.Handle("/config", r.displayConfig)
	r.TelegramBot.Handle("/validators", func(message *tb.Message) {
		r.getValidatorsStatus(message, false)
	})
	r.TelegramBot.Handle("/missing", func(message *tb.Message) {
		r.getValidatorsStatus(message, true)
	})
	r.TelegramBot.Handle("/params", r.getChainParams)
	go r.TelegramBot.Start()

	r.loadConfigFromYaml()
}

func (r TelegramReporter) Enabled() bool {
	return r.TelegramBot != nil
}

func (r TelegramReporter) SendReport(report Report) error {
	serializedReport := r.Serialize(report)
	_, err := r.TelegramBot.Send(
		&tb.User{
			ID: r.TelegramAppConfig.Chat,
		},
		serializedReport,
		tb.ModeHTML,
		tb.NoPreview,
	)
	return err
}

func (r TelegramReporter) Name() string {
	return "TelegramReporter"
}

func (r TelegramReporter) sendMessage(message *tb.Message, text string) {
	msgsByNewline := strings.Split(text, "\n")

	var sb strings.Builder

	for _, line := range msgsByNewline {
		if sb.Len()+len(line) > MaxMessageSize {
			if _, err := r.TelegramBot.Send(
				message.Chat,
				sb.String(),
				&tb.SendOptions{
					ParseMode:             tb.ModeHTML,
					ReplyTo:               message,
					DisableWebPagePreview: true,
				},
				tb.NoPreview,
			); err != nil {
				log.Error().Err(err).Msg("Could not send Telegram message")
			}

			sb.Reset()
		}

		sb.WriteString(line + "\n")
	}

	if sb.Len() == 0 {
		return
	}

	if _, err := r.TelegramBot.Send(
		message.Chat,
		sb.String(),
		&tb.SendOptions{
			ParseMode:             tb.ModeHTML,
			ReplyTo:               message,
			DisableWebPagePreview: true,
		},
		tb.NoPreview,
	); err != nil {
		log.Error().Err(err).Msg("Could not send Telegram message")
	}
}

func (r TelegramReporter) getHelp(message *tb.Message) {
	var sb strings.Builder
	sb.WriteString("<strong>missed-block-checker</strong>\n\n")
	sb.WriteString(fmt.Sprintf("Query for the %s network info.\n", r.ChainInfoConfig.MintscanPrefix))
	sb.WriteString("Can understand the following commands:\n")
	sb.WriteString("- /subscribe &lt;validator address&gt; - be notified on validator's missed block in a Telegram channel\n")
	sb.WriteString("- /unsubscribe &lt;validator address&gt; - undo the subscription given at the previous step\n")
	sb.WriteString("- /status &lt;validator address&gt; - get validator missed blocks\n")
	sb.WriteString("- /status - get the missed blocks of the validator(s) you're subscribed to\n\n")
	sb.WriteString("- /config - display bot config\n")
	sb.WriteString("- /params - display chain slashing params\n")
	sb.WriteString("- /validators - display all active validators and their missed blocks\n")
	sb.WriteString("- /missing - display only validators missing blocks above threshold and their missing blocks\n")
	sb.WriteString("Created by <a href=\"https://freak12techno.github.io\">freak12techno</a> at <a href=\"https://validator.solar\">SOLAR Labs</a> with ❤️.\n")
	sb.WriteString("This bot is open-sourced, you can get the source code at https://github.com/solarlabsteam/missed-blocks-checker.\n\n")
	sb.WriteString("We also maintain the following tools for Cosmos ecosystem:\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-interacter\">cosmos-interacter</a> - a bot that can return info about Cosmos-based blockchain params.\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-exporter\">cosmos-exporter</a> - scrape the blockchain data from the local node and export it to Prometheus\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/coingecko-exporter\">coingecko-exporter</a> - scrape the Coingecko exchange rate and export it to Prometheus\n")
	sb.WriteString("- <a href=\"https://github.com/solarlabsteam/cosmos-transactions-bot\">cosmos-transactions-bot</a> - monitor the incoming transactions for a given filter\n\n")
	sb.WriteString("If you like what we're doing, consider <a href=\"https://validator.solar\">staking with us</a>!\n")

	r.sendMessage(message, sb.String())
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned help info")
}

func (r *TelegramReporter) getValidatorStatus(message *tb.Message) {
	args := strings.SplitAfterN(message.Text, " ", 2)
	if len(args) < 2 {
		r.getSubscribedValidatorsStatuses(message)
		return
	}

	address := args[1]
	r.Logger.Debug().Str("address", address).Msg("getValidatorStatus: address")

	state, err := r.Client.GetValidatorState(address)
	if err != nil {
		r.Logger.Error().
			Str("address", address).
			Err(err).
			Msg("Could not get validators")
		r.sendMessage(message, "Could not find validator")
		return
	}

	r.sendMessage(message, r.getValidatorWithMissedBlocksSerialized(state))
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Str("address", address).
		Msg("Successfully returned validator status")
}

func (r *TelegramReporter) getValidatorsStatus(message *tb.Message, getOnlyMissing bool) {
	state, err := r.Client.GetValidatorsState()
	if err != nil {
		r.Logger.Error().
			Err(err).
			Msg("Could not get validators state")
		r.sendMessage(message, "Could not get validators state")
		return
	}

	state = FilterMap(state, func(s ValidatorState) bool {
		if getOnlyMissing {
			group, err := r.AppConfig.MissedBlocksGroups.GetGroup(s.MissedBlocks)
			if err != nil {
				r.Logger.Error().
					Err(err).
					Msg("Could not get validator missed block group")
				return !s.Jailed
			}

			return !s.Jailed && group.Start != 0
		}

		return !s.Jailed
	})

	stateArray := MapToSlice(state)
	sort.SliceStable(stateArray, func(i, j int) bool {
		return stateArray[i].MissedBlocks < stateArray[j].MissedBlocks
	})

	sendMessage, err := r.getValidatorsWithMissedBlocksSerialized(stateArray)
	if err != nil {
		r.Logger.Error().
			Err(err).
			Msg("Error serializing validators")
		r.sendMessage(message, "Error serializing response")
		return
	}

	r.sendMessage(message, sendMessage)
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned validators status")
}

func (r *TelegramReporter) getChainParams(message *tb.Message) {
	params := r.Client.GetSlashingParams()
	sendMessage := r.getChainParamsSerialized(params, r.Params)

	r.sendMessage(message, sendMessage)
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned validators status")
}

func (r *TelegramReporter) getSubscribedValidatorsStatuses(message *tb.Message) {
	r.Logger.Debug().Msg("getSubscribedValidatorsStatuses")

	subscribedValidators := r.TelegramConfig.getNotifiedValidators(message.Sender.Username)
	if len(subscribedValidators) == 0 {
		r.sendMessage(message, "You are not subscribed to any validator's missed blocks notifications.")
		return
	}

	var sb strings.Builder

	for _, address := range subscribedValidators {
		state, err := r.Client.GetValidatorState(address)
		if err != nil {
			r.Logger.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get validators")
			r.sendMessage(message, "Could not find validator")
			return
		}

		sb.WriteString(r.getValidatorWithMissedBlocksSerialized(state))
		sb.WriteString("\n")
	}

	r.sendMessage(message, sb.String())
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned subscribed validator statuses")
}

func (r *TelegramReporter) getValidatorWithMissedBlocksSerialized(state ValidatorState) string {
	var sb strings.Builder
	sb.WriteString(r.ChainInfoConfig.GetValidatorPage(state.Address, state.Moniker) + "\n")
	sb.WriteString(fmt.Sprintf(
		"Missed blocks: %d/%d (%.2f%%)\n",
		state.MissedBlocks,
		r.Params.SignedBlocksWindow,
		float64(state.MissedBlocks)/float64(r.Params.SignedBlocksWindow)*100,
	))

	return sb.String()
}

func (r *TelegramReporter) getValidatorsWithMissedBlocksSerialized(state []ValidatorState) (string, error) {
	var sb strings.Builder

	for _, validator := range state {
		group, err := r.AppConfig.MissedBlocksGroups.GetGroup(validator.MissedBlocks)
		if err != nil {
			return "", err
		}

		sb.WriteString(fmt.Sprintf(
			"%s %s (%.2f%%)\n",
			group.EmojiEnd,
			r.ChainInfoConfig.GetValidatorPage(validator.Address, validator.Moniker),
			float64(validator.MissedBlocks)/float64(r.Params.SignedBlocksWindow)*100,
		))
	}

	return sb.String(), nil
}

func (r *TelegramReporter) getChainParamsSerialized(
	slashingParams SlashingParams,
	params *Params,
) string {
	nanoSecondsToJail := float64(slashingParams.MissedBlocksToJail) * params.AvgBlockTime * 1_000_000_000
	durationToJail := time.Duration(math.Floor(nanoSecondsToJail))

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<strong>Blocks window</strong>: %d\n", slashingParams.SignedBlocksWindow))
	sb.WriteString(fmt.Sprintf(
		"<strong>Validator needs to sign</strong> %.2f%%, or %d blocks in this window.\n",
		slashingParams.MinSignedPerWindow*100,
		slashingParams.MissedBlocksToJail,
	))
	sb.WriteString(fmt.Sprintf(
		"<strong>Slashing factor for downtime slashing:</strong> %.2f%%\n",
		slashingParams.SlashFractionDowntime*100,
	))
	sb.WriteString(fmt.Sprintf(
		"<strong>Slashing factor for double sign:</strong> %.2f%%\n",
		slashingParams.SlashFractionDoubleSign*100,
	))
	sb.WriteString(fmt.Sprintf(
		"<strong>Average block time:</strong> %.2f seconds\n",
		params.AvgBlockTime,
	))
	sb.WriteString(fmt.Sprintf(
		"<strong>Approximate time to go to jail when missing all blocks:</strong> %s\n",
		durationToJail,
	))

	return sb.String()
}

func (r *TelegramReporter) subscribeToValidatorUpdates(message *tb.Message) {
	if message.Sender.Username == "" {
		r.sendMessage(message, "Please set your Telegram username first.")
		return
	}

	args := strings.SplitAfterN(message.Text, " ", 2)
	if len(args) < 2 {
		r.sendMessage(message, "Usage: /subscribe &lt;validator address&gt;")
		return
	}

	address := args[1]
	r.Logger.Debug().Str("address", address).Msg("subscribeToValidatorUpdates: address")

	validator, err := r.Client.GetValidator(address)
	if err != nil {
		r.Logger.Error().
			Str("address", address).
			Err(err).
			Msg("Could not get validator")
		r.sendMessage(message, "Could not find validator")
		return
	}

	err = r.TelegramConfig.addNotifier(address, message.Sender.Username)
	r.saveYamlConfig()

	if err != nil {
		r.sendMessage(message, err.Error())
		r.saveYamlConfig()
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Subscribed to the notification of <code>%s</code> ", validator.Description.Moniker))
	sb.WriteString(r.ChainInfoConfig.GetValidatorPage(validator.OperatorAddress, "Explorer"))

	r.sendMessage(message, sb.String())
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Str("address", address).
		Msg("Successfully subscribed to validator's notifications.")
}

func (r *TelegramReporter) unsubscribeFromValidatorUpdates(message *tb.Message) {
	if message.Sender.Username == "" {
		r.sendMessage(message, "Please set your Telegram username first.")
		return
	}

	args := strings.SplitAfterN(message.Text, " ", 2)
	if len(args) < 2 {
		r.sendMessage(message, "Usage: /unsubscribe &lt;validator address&gt;")
		return
	}

	address := args[1]
	r.Logger.Debug().Str("address", address).Msg("unsubscribeFromValidatorUpdates: address")

	validator, err := r.Client.GetValidator(address)
	if err != nil {
		r.Logger.Error().
			Str("address", address).
			Err(err).
			Msg("Could not get validator")
		r.sendMessage(message, "Could not find validator")
		return
	}

	err = r.TelegramConfig.removeNotifier(address, message.Sender.Username)
	r.saveYamlConfig()

	if err != nil {
		r.sendMessage(message, err.Error())
		r.saveYamlConfig()
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Unsubscribed from the notification of <code>%s</code> ", validator.Description.Moniker))
	sb.WriteString(r.ChainInfoConfig.GetValidatorPage(validator.OperatorAddress, "Explorer"))

	r.sendMessage(message, sb.String())
	r.Logger.Info().
		Str("user", message.Sender.Username).
		Str("address", address).
		Msg("Successfully unsubscribed from validator's notifications.")
}

func (r *TelegramReporter) displayConfig(message *tb.Message) {
	var sb strings.Builder

	if len(r.AppConfig.ExcludeValidators) == 0 && len(r.AppConfig.IncludeValidators) == 0 {
		sb.WriteString("<strong>Monitoring all validators.\n</strong>")
	} else if len(r.AppConfig.IncludeValidators) == 0 {
		sb.WriteString("<strong>Monitoring all validators, except the following ones:\n</strong>")

		for _, validator := range r.AppConfig.ExcludeValidators {
			sb.WriteString(" - " + r.ChainInfoConfig.GetValidatorPage(validator, validator) + "\n")
		}
	} else if len(r.AppConfig.ExcludeValidators) == 0 {
		sb.WriteString("<strong>Monitoring the following validators:\n</strong>")

		for _, validator := range r.AppConfig.IncludeValidators {
			sb.WriteString("- " + r.ChainInfoConfig.GetValidatorPage(validator, validator) + "\n")
		}
	}

	sb.WriteString("<strong>Missed blocks thresholds:\n</strong>")
	for _, group := range r.AppConfig.MissedBlocksGroups {
		sb.WriteString(fmt.Sprintf("%s %d - %d\n", group.EmojiStart, group.Start, group.End))
	}

	r.sendMessage(message, sb.String())
}

func (r *TelegramReporter) loadConfigFromYaml() {
	if _, err := os.Stat(r.TelegramAppConfig.ConfigPath); os.IsNotExist(err) {
		r.Logger.Info().Str("path", r.TelegramAppConfig.ConfigPath).Msg("Telegram config file does not exist, creating.")
		if _, err = os.Create(r.TelegramAppConfig.ConfigPath); err != nil {
			r.Logger.Fatal().Err(err).Msg("Could not create Telegram config!")
		}
	} else if err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not fetch Telegram config!")
	}

	bytes, err := ioutil.ReadFile(r.TelegramAppConfig.ConfigPath)
	if err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not read Telegram config!")
	}

	var conf TelegramConfig
	if _, err := toml.Decode(string(bytes), &conf); err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not load Telegram config!")
	}

	r.TelegramConfig = conf
	r.Logger.Debug().Msg("Telegram config is loaded successfully.")
}

func (r *TelegramReporter) saveYamlConfig() {
	f, err := os.Create(r.TelegramAppConfig.ConfigPath)
	if err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not open Telegram config when saving")
	}
	if err := toml.NewEncoder(f).Encode(r.TelegramConfig); err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not save Telegram config")
	}
	if err := f.Close(); err != nil {
		r.Logger.Fatal().Err(err).Msg("Could not close Telegram config when saving")
	}

	r.Logger.Debug().Msg("Telegram config is updated successfully.")
}
