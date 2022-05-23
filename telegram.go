package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/BurntSushi/toml"
	tb "gopkg.in/tucnak/telebot.v2"
)

type TelegramReporter struct {
	TelegramToken      string
	TelegramChat       int
	TelegramConfigPath string
	TelegramConfig     TelegramConfig

	TelegramBot *tb.Bot
}

type NotificationInfo struct {
	ValidatorAddress string
	Notifiers        []string
}

type TelegramConfig struct {
	NotiticationInfos []*NotificationInfo
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
			timeToJail    string = ""
		)

		switch entry.Direction {
		case INCREASING:
			timeToJail = fmt.Sprintf(" (%s till jail)", entry.GetTimeToJail())
		}

		validatorLink = fmt.Sprintf(
			"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
			Config.MintscanPrefix,
			entry.ValidatorAddress,
			entry.ValidatorMoniker,
		)

		notifiers := r.TelegramConfig.getNotifiersSerialized(entry.ValidatorAddress)

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>%s %s\n",
			entry.Emoji,
			validatorLink,
			entry.Description,
			timeToJail,
			notifiers,
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
		Token:  Config.TelegramToken,
		Poller: &tb.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Warn().Err(err).Msg("Could not create Telegram bot")
		return
	}

	r.TelegramBot = bot
	r.TelegramBot.Handle("/start", r.getHelp)
	r.TelegramBot.Handle("/help", r.getHelp)
	r.TelegramBot.Handle("/status", r.getValidatorStatus)
	r.TelegramBot.Handle("/subscribe", r.subscribeToValidatorUpdates)
	r.TelegramBot.Handle("/unsubscribe", r.unsubscribeFromValidatorUpdates)
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
			ID: r.TelegramChat,
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
	_, err := r.TelegramBot.Send(
		message.Chat,
		text,
		&tb.SendOptions{
			ParseMode:             tb.ModeHTML,
			ReplyTo:               message,
			DisableWebPagePreview: true,
		},
		tb.NoPreview,
	)
	if err != nil {
		log.Error().Err(err).Msg("Could not send Telegram message")
	}
}

func (r TelegramReporter) getHelp(message *tb.Message) {
	var sb strings.Builder
	sb.WriteString("<strong>missed-block-checker</strong>\n\n")
	sb.WriteString(fmt.Sprintf("Query for the %s network info.\n", Config.MintscanPrefix))
	sb.WriteString("Can understand the following commands:\n")
	sb.WriteString("- /subscribe &lt;validator address&gt; - be notified on validator's missed block in a Telegram channel\n")
	sb.WriteString("- /unsubscribe &lt;validator address&gt; - undo the subscription given at the previous step\n")
	sb.WriteString("- /status &lt;validator address&gt; - get validator missed blocks\n")
	sb.WriteString("- /status - get the missed blocks of the validator(s) you're subscribed to\n\n")
	sb.WriteString("Created by <a href=\"https://freak12techno.github.io\">freak12techno</a> at <a href=\"https://validator.solar\">SOLAR Labs</a> with ❤️.\n")
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

func (r *TelegramReporter) getValidatorStatus(message *tb.Message) {
	args := strings.SplitAfterN(message.Text, " ", 2)
	if len(args) < 2 {
		r.getSubscribedValidatorsStatuses(message)
		return
	}

	address := args[1]
	log.Debug().Str("address", address).Msg("getValidatorStatus: address")

	validator, err := getValidator(address)
	if err != nil {
		log.Error().
			Str("address", address).
			Err(err).
			Msg("Could not get validators")
		r.sendMessage(message, "Could not find validator")
		return
	}

	signingInfo, err := getSigningInfo(validator)
	if err != nil {
		r.sendMessage(message, "Could not get missed blocks info")
		return
	}

	r.sendMessage(message, getValidatorWithMissedBlocksSerialized(validator, signingInfo))
	log.Info().
		Str("user", message.Sender.Username).
		Str("address", address).
		Msg("Successfully returned validator status")
}

func (r *TelegramReporter) getSubscribedValidatorsStatuses(message *tb.Message) {
	log.Debug().Msg("getSubscribedValidatorsStatuses")

	subscribedValidators := r.TelegramConfig.getNotifiedValidators(message.Sender.Username)
	if len(subscribedValidators) == 0 {
		r.sendMessage(message, "You are not subscribed to any validator's missed blocks notifications.")
		return
	}

	var sb strings.Builder

	for _, address := range subscribedValidators {
		validator, err := getValidator(address)
		if err != nil {
			log.Error().
				Str("address", address).
				Err(err).
				Msg("Could not get validators")
			r.sendMessage(message, "Could not find validator")
			return
		}

		signingInfo, err := getSigningInfo(validator)
		if err != nil {
			r.sendMessage(message, "Could not get missed blocks info")
			return
		}

		sb.WriteString(getValidatorWithMissedBlocksSerialized(validator, signingInfo))
		sb.WriteString("\n")
	}

	r.sendMessage(message, sb.String())
	log.Info().
		Str("user", message.Sender.Username).
		Msg("Successfully returned subscribed validator statuses")
}

func getValidatorWithMissedBlocksSerialized(validator stakingtypes.Validator, signingInfo slashingtypes.ValidatorSigningInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<code>%s</code>\n", validator.Description.Moniker))
	sb.WriteString(fmt.Sprintf(
		"Missed blocks: %d/%d (%.2f%%)\n",
		signingInfo.MissedBlocksCounter,
		SignedBlocksWindow,
		float64(signingInfo.MissedBlocksCounter)/float64(SignedBlocksWindow)*100,
	))
	sb.WriteString(fmt.Sprintf(
		"<a href=\"https://mintscan.io/%s/validators/%s\">Mintscan</a>\n",
		Config.MintscanPrefix,
		validator.OperatorAddress,
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
	log.Debug().Str("address", address).Msg("subscribeToValidatorUpdates: address")

	validator, err := getValidator(address)
	if err != nil {
		log.Error().
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
	sb.WriteString(fmt.Sprintf(
		"<a href=\"https://mintscan.io/%s/validators/%s\">Mintscan</a>\n",
		Config.MintscanPrefix,
		validator.OperatorAddress,
	))

	r.sendMessage(message, sb.String())
	log.Info().
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
	log.Debug().Str("address", address).Msg("unsubscribeFromValidatorUpdates: address")

	validator, err := getValidator(address)
	if err != nil {
		log.Error().
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
	sb.WriteString(fmt.Sprintf(
		"<a href=\"https://mintscan.io/%s/validators/%s\">Mintscan</a>\n",
		Config.MintscanPrefix,
		validator.OperatorAddress,
	))

	r.sendMessage(message, sb.String())
	log.Info().
		Str("user", message.Sender.Username).
		Str("address", address).
		Msg("Successfully unsubscribed from validator's notifications.")
}

func (r *TelegramReporter) loadConfigFromYaml() {
	if _, err := os.Stat(r.TelegramConfigPath); os.IsNotExist(err) {
		log.Info().Str("path", r.TelegramConfigPath).Msg("Telegram config file does not exist, creating.")
		if _, err = os.Create(r.TelegramConfigPath); err != nil {
			log.Fatal().Err(err).Msg("Could not create Telegram config!")
		}
	} else if err != nil {
		log.Fatal().Err(err).Msg("Could not fetch Telegram config!")
	}

	bytes, err := ioutil.ReadFile(r.TelegramConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not read Telegram config!")
	}

	var conf TelegramConfig
	if _, err := toml.Decode(string(bytes), &conf); err != nil {
		log.Fatal().Err(err).Msg("Could not load Telegram config!")
	}

	r.TelegramConfig = conf
	log.Debug().Msg("Telegram config is loaded successfully.")
}

func (r *TelegramReporter) saveYamlConfig() {
	f, err := os.Create(r.TelegramConfigPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not open Telegram config when saving")
	}
	if err := toml.NewEncoder(f).Encode(r.TelegramConfig); err != nil {
		log.Fatal().Err(err).Msg("Could not save Telegram config")
	}
	if err := f.Close(); err != nil {
		log.Fatal().Err(err).Msg("Could not close Telegram config when saving")
	}

	log.Debug().Msg("Telegram config is updated successfully.")
}

func getValidator(address string) (stakingtypes.Validator, error) {
	stakingClient := stakingtypes.NewQueryClient(grpcConn)

	validatorResponse, err := stakingClient.Validator(
		context.Background(),
		&stakingtypes.QueryValidatorRequest{ValidatorAddr: address},
	)
	if err != nil {
		return stakingtypes.Validator{}, err
	}

	return validatorResponse.Validator, nil
}

func getSigningInfo(validator stakingtypes.Validator) (slashingtypes.ValidatorSigningInfo, error) {
	slashingClient := slashingtypes.NewQueryClient(grpcConn)

	err := validator.UnpackInterfaces(interfaceRegistry) // Unpack interfaces, to populate the Anys' cached values
	if err != nil {
		log.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get unpack validator inferfaces")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	pubKey, err := validator.GetConsAddr()
	if err != nil {
		log.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get validator pubkey")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	signingInfosResponse, err := slashingClient.SigningInfo(
		context.Background(),
		&slashingtypes.QuerySigningInfoRequest{ConsAddress: pubKey.String()},
	)
	if err != nil {
		log.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get signing info")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	return signingInfosResponse.ValSigningInfo, nil
}
