package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"

	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/rs/zerolog"
	telegramBot "gopkg.in/tucnak/telebot.v2"
)

var NodeAddress = flag.String("node", "localhost:9090", "RPC node address")
var LogLevel = flag.String("log-level", "info", "Logging level")
var TelegramToken = flag.String("telegram-token", "", "Telegram bot token")
var TelegramChat = flag.Int("telegram-chat", 0, "Telegram chat or user ID")
var Interval = flag.Int("interval", 120, "Interval between two checks, in seconds")
var Threshold = flag.Int64("threshold", 0, "Threshold of missed blocks")
var Limit = flag.Uint64("limit", 1000, "gRPC query pagination limit")
var MintscanPrefix = flag.String("mintscan", "persistence", "Prefix for mintscan links like https://mintscan.io/{prefix}")

var PrefixFlag = flag.String("bech-prefix", "persistence", "Bech32 global prefix")

// some networks, like Iris, have the different prefixes for address, validator and consensus node
var ValidatorPrefixFlag = flag.String("bech-validator-prefix", "", "Bech32 validator prefix")
var ValidatorPubkeyPrefixFlag = flag.String("bech-validator-pubkey-prefix", "", "Bech32 pubkey validator prefix")
var ConsensusNodePrefixFlag = flag.String("bech-consensus-node-prefix", "", "Bech32 consensus node prefix")
var ConsensusNodePubkeyPrefixFlag = flag.String("bech-consensus-node-pubkey-prefix", "", "Bech32 pubkey consensus node prefix")

var ValidatorPrefix string
var ValidatorPubkeyPrefix string
var ConsensusNodePrefix string
var ConsensusNodePubkeyPrefix string

var log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()

var beforePreviousInfos []slashingtypes.ValidatorSigningInfo
var previousInfos []slashingtypes.ValidatorSigningInfo

var validators []stakingtypes.Validator

var bot *telegramBot.Bot

var encCfg = simapp.MakeTestEncodingConfig()
var interfaceRegistry = encCfg.InterfaceRegistry

func main() {
	flag.Parse()

	logLevel, err := zerolog.ParseLevel(*LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not parse log level")
	}

	zerolog.SetGlobalLevel(logLevel)

	bot, err = telegramBot.NewBot(telegramBot.Settings{
		Token:  *TelegramToken,
		Poller: &telegramBot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Could not create Telegram bot")
		return
	}

	if *ValidatorPrefixFlag == "" {
		ValidatorPrefix = *PrefixFlag + "valoper"
	} else {
		ValidatorPrefix = *ValidatorPrefixFlag
	}

	if *ValidatorPubkeyPrefixFlag == "" {
		ValidatorPubkeyPrefix = *PrefixFlag + "valoperpub"
	} else {
		ValidatorPubkeyPrefix = *ValidatorPubkeyPrefixFlag
	}

	if *ConsensusNodePrefixFlag == "" {
		ConsensusNodePrefix = *PrefixFlag + "valcons"
	} else {
		ConsensusNodePrefix = *ConsensusNodePrefixFlag
	}

	if *ConsensusNodePubkeyPrefixFlag == "" {
		ConsensusNodePubkeyPrefix = *PrefixFlag + "valconspub"
	} else {
		ConsensusNodePubkeyPrefix = *ConsensusNodePrefixFlag
	}

	config := sdk.GetConfig()
	config.SetBech32PrefixForValidator(ValidatorPrefix, ValidatorPubkeyPrefix)
	config.SetBech32PrefixForConsensusNode(ConsensusNodePrefix, ConsensusNodePubkeyPrefix)
	config.Seal()

	log.Info().
		Str("--node", *NodeAddress).
		Str("--log-level", *LogLevel).
		Int("--interval", *Interval).
		Int64("--threshold", *Threshold).
		Uint64("--limit", *Limit).
		Str("--bech-validator-prefix", ValidatorPrefix).
		Str("--bech-validator-pubkey-prefix", ValidatorPubkeyPrefix).
		Str("--bech-consensus-node-prefix", ConsensusNodePrefix).
		Str("--bech-consensus-node-pubkey-prefix", ConsensusNodePubkeyPrefix).
		Msg("Started with following parameters")

	grpcConn, err := grpc.Dial(
		*NodeAddress,
		grpc.WithInsecure(),
	)
	if err != nil {
		panic(err)
	}

	defer grpcConn.Close()

	for {
		checkValidators(grpcConn)
		time.Sleep(time.Duration(*Interval) * time.Second)
	}
}

func checkValidators(grpcConn *grpc.ClientConn) {
	log.Trace().Msg("=============== Request start =================")
	defer log.Trace().Msg("=============== Request end =================")

	slashingClient := slashingtypes.NewQueryClient(grpcConn)
	signingInfos, err := slashingClient.SigningInfos(
		context.Background(),
		&slashingtypes.QuerySigningInfosRequest{
			Pagination: &querytypes.PageRequest{
				Limit: *Limit,
			},
		},
	)

	if err != nil {
		log.Error().Err(err).Msg("Could not query for signing info")
		return
	}

	stakingClient := stakingtypes.NewQueryClient(grpcConn)
	validatorsResult, err := stakingClient.Validators(
		context.Background(),
		&stakingtypes.QueryValidatorsRequest{
			Pagination: &querytypes.PageRequest{
				Limit: *Limit,
			},
		},
	)

	if err != nil {
		log.Error().Err(err).Msg("Could not query for validators")
		return
	}

	validators = validatorsResult.Validators

	log.Trace().Msg("Validators list:")
	for _, signingInfo := range signingInfos.Info {
		log.Trace().
			Str("address", signingInfo.Address).
			Int64("startHeight", signingInfo.StartHeight).
			Int64("missedBlocks", signingInfo.MissedBlocksCounter).
			Msg("-- Validator info")
	}

	if previousInfos == nil {
		log.Info().Msg("Previous infos is empty, first start. No checking difference")
		beforePreviousInfos = previousInfos
		previousInfos = signingInfos.Info
		return
	}

	var sb strings.Builder

	missedBlocksIncreased := 0
	missedBlocksDecreased := 0
	missedBlocksNotChanged := 0

	log.Trace().Msg("Processing validators")
	for _, signingInfo := range signingInfos.Info {
		var previousInfo slashingtypes.ValidatorSigningInfo
		previousInfoFound := false
		for _, previousInfoIterated := range previousInfos {
			if previousInfoIterated.Address == signingInfo.Address {
				previousInfo = previousInfoIterated
				previousInfoFound = true
				break
			}
		}

		if !previousInfoFound {
			log.Debug().Str("address", signingInfo.Address).Msg("-- Could not find previous info")
			continue
		}

		// if it's zero - validator hasn't missed any blocks since last check
		// if it's > 0 - validator has missed some blocks since last check
		// if it's < 0 - validator has missed some blocks in the past
		// but the window is moving and they are not missing blocks now.
		previous := previousInfo.MissedBlocksCounter
		current := signingInfo.MissedBlocksCounter
		diff := current - previous

		if current <= *Threshold && previous <= *Threshold {
			continue
		}

		var validatorLink string

		// somehow not all the validators info is returned
		if validator, found := findValidator(signingInfo.Address); found {
			validatorLink = fmt.Sprintf(
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				*MintscanPrefix,
				validator.OperatorAddress,
				validator.Description.Moniker,
			)
		} else {
			log.Debug().Str("address", signingInfo.Address).Msg("-- Could not find validator")
			validatorLink = fmt.Sprintf("validator with key %s", signingInfo.Address)
		}

		log.Debug().
			Str("address", signingInfo.Address).
			Int64("missedBlocks", diff).
			Int64("before", previous).
			Int64("after", current).
			Msg("-- Validator diff with previous state")

		var emoji string
		var status string

		// Possible cases:
		// 1) previous state < threshold, current state < threshold - validator is not missing blocks, ignoring
		// 2) previous state < threshold, current state > threshold - validator started missing blocks
		// 3) previous state > threshold, current state > threshold, diff > 0 - validator is still missing blocks
		// 4) previous state > threshold, current state > threshold, diff == 0 - validator stopped missing blocks
		// 5) previous state > threshold, current state > threshold, diff < 0 - window is moving
		// 6) previous state > threshold, current state < threshold - window moved, validator is back to normal

		if current > *Threshold && previous <= *Threshold {
			// 2
			emoji = "🚨"
			status = "started missing blocks"
			log.Debug().Msg("---- Validator started missing blocks")
			missedBlocksIncreased += 1
		} else if current > *Threshold && previous > *Threshold && diff > 0 {
			// 3
			emoji = "🔴"
			status = "is still missing blocks"
			log.Debug().Msg("---- is still missing blocks")
			missedBlocksIncreased += 1
		} else if current > *Threshold && previous > *Threshold && diff == 0 {
			// 4
			// This is where it gets crazy: we need to check not the previous state,
			// but the one before it, to see if we've sent any notifications redarding that.
			log.Debug().Msg("---- Validator stopped missing blocks")
			missedBlocksNotChanged += 1

			var beforePreviousInfo slashingtypes.ValidatorSigningInfo
			beforePreviousInfoFound := false
			for _, beforePreviousInfoIterated := range beforePreviousInfos {
				if beforePreviousInfoIterated.Address == signingInfo.Address {
					beforePreviousInfo = beforePreviousInfoIterated
					beforePreviousInfoFound = true
					break
				}
			}

			if !beforePreviousInfoFound {
				log.Debug().Msg("---- Could not find before previous info")
				continue
			}

			// Now, if current diff is zero, but diff between previous and before previous is not zero,
			// that means we haven't sent a notification so far, and should do it.
			previousDiff := previousInfo.MissedBlocksCounter - beforePreviousInfo.MissedBlocksCounter
			if previousDiff == 0 {
				log.Debug().Msg("---- Previous diff == 0, notification already sent.")
				continue
			}

			emoji = "🟡"
			status = "stopped missing blocks"
		} else if current > *Threshold && previous > *Threshold && diff < 0 {
			// 5
			log.Debug().Msg("---- Window is moving, diff is negative")
			missedBlocksDecreased += 1
			continue
		} else if current <= *Threshold && previous > *Threshold && diff < 0 {
			missedBlocksDecreased += 1
			// 6
			emoji = "🟢"
			status = "went back to normal"
		} else {
			log.Fatal().Msg("Unexpected state")
		}

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s</strong> %s: %d -> %d\n",
			emoji,
			validatorLink,
			status,
			previousInfo.MissedBlocksCounter,
			signingInfo.MissedBlocksCounter,
		))

	}

	log.Info().
		Int("missedBlocksIncreased", missedBlocksIncreased).
		Int("missedBlocksNotChanged", missedBlocksNotChanged).
		Int("missedBlocksDecreased", missedBlocksDecreased).
		Msg("Validators diff")

	tgMessage := sb.String()

	if tgMessage != "" {
		log.Debug().Str("msg", sb.String()).Msg("Formatted string")
		_, err = bot.Send(&telegramBot.User{ID: *TelegramChat}, sb.String(), telegramBot.ModeHTML)
		if err != nil {
			log.Error().
				Err(err).
				Msg("Could not send Telegram message")
			return
		}
	}

	beforePreviousInfos = previousInfos
	previousInfos = signingInfos.Info
}

func findValidator(address string) (stakingtypes.Validator, bool) {
	for _, validatorIterated := range validators {
		err := validatorIterated.UnpackInterfaces(interfaceRegistry)
		if err != nil {
			// shouldn't happen
			log.Error().Err(err).Msg("Could not unpack interface")
			return stakingtypes.Validator{}, false
		}

		pubKey, err := validatorIterated.GetConsAddr()
		if err != nil {
			log.Error().
				Str("address", validatorIterated.OperatorAddress).
				Err(err).
				Msg("Could not get validator pubkey")
		}

		if pubKey.String() == address {
			return validatorIterated, true
		}
	}

	return stakingtypes.Validator{}, false
}
