package main

import (
	"fmt"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/rs/zerolog"
)

type ReportGenerator struct {
	Params   Params
	Config   *AppConfig
	gRPC     *TendermintGRPC
	Logger   zerolog.Logger
	State    ValidatorsState
	Registry codectypes.InterfaceRegistry
}

func NewReportGenerator(
	params Params,
	grpc *TendermintGRPC,
	config *AppConfig,
	logger *zerolog.Logger,
	registry codectypes.InterfaceRegistry,
) *ReportGenerator {
	return &ReportGenerator{
		Params:   params,
		gRPC:     grpc,
		Config:   config,
		Logger:   logger.With().Str("component", "report_generator").Logger(),
		Registry: registry,
	}
}

func (g *ReportGenerator) GetNewState() (ValidatorsState, error) {
	g.Logger.Debug().Msg("Querying for signing infos...")

	state, err := g.gRPC.GetValidatorsState()
	if err != nil {
		g.Logger.Error().Err(err).Msg("Could not query for signing infos")
		return nil, err
	}

	return FilterMap(state, func(v ValidatorState) bool {
		return g.Config.IsValidatorMonitored(v.Address)
	}), nil
}

func (g *ReportGenerator) GetValidatorReportEntry(oldState, newState ValidatorState) (*ReportEntry, bool) {
	g.Logger.Trace().
		Str("oldState", fmt.Sprintf("%+v", oldState)).
		Str("newState", fmt.Sprintf("%+v", newState)).
		Msg("Processing validator report entry")

	// 1. If validator's tombstoned, but wasn't - set tombstoned report entry.
	if newState.Tombstoned && !oldState.Tombstoned {
		g.Logger.Debug().
			Str("address", oldState.Address).
			Msg("Validator is tombstoned")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            TombstonedEmoji,
			Description:      TombstonedDesc,
			Direction:        TOMBSTONED,
		}, true
	}

	// 2. If validator's jailed, but wasn't - set jailed report entry.
	if newState.Jailed && !oldState.Jailed {
		g.Logger.Debug().
			Str("address", oldState.Address).
			Msg("Validator is jailed")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            JailedEmoju,
			Description:      JailedDesc,
			Direction:        JAILED,
		}, true
	}

	// 3. If validator's not jailed, but was - set unjailed report entry.
	if !newState.Jailed && oldState.Jailed {
		g.Logger.Debug().
			Str("address", oldState.Address).
			Msg("Validator is unjailed")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            UnjailedEmoji,
			Description:      UnjailedDesc,
			Direction:        UNJAILED,
		}, true
	}

	// 4. If validator is and was jailed - do nothing.
	if newState.Jailed && oldState.Jailed {
		g.Logger.Debug().
			Str("address", oldState.Address).
			Msg("Validator is and was jailed - no need to send report")
		return nil, false
	}

	// 5. Validator isn't and wasn't jailed.
	//
	// First, check if old and new groups are the same - if they have different start,
	// they are different. If they don't - they aren't so no need to send a notification.
	oldGroup, oldGroupErr := g.Config.MissedBlocksGroups.GetGroup(oldState.MissedBlocks)
	if oldGroupErr != nil {
		g.Logger.Error().Err(oldGroupErr).Msg("Could not get old group")
		return nil, false
	}
	newGroup, newGroupErr := g.Config.MissedBlocksGroups.GetGroup(newState.MissedBlocks)
	if newGroupErr != nil {
		g.Logger.Error().Err(newGroupErr).Msg("Could not get new group")
		return nil, false
	}

	if oldGroup.Start == newGroup.Start {
		g.Logger.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator didn't change group - no need to send report")
		return nil, false
	}

	// Validator switched from one MissedBlockGroup to another, 2 cases how that may happen
	// 1) validator is skipping blocks
	// 2) validator skipped some blocks in the past, but recovered, is now signing, and the window
	// moves - the amount of missed blocks is decreasing.
	// Need to understand which one it is: if old missed blocks < new missed blocks -
	// it's 1), if vice versa, then 2)

	entry := &ReportEntry{
		ValidatorAddress: newState.Address,
		ValidatorMoniker: newState.Moniker,
		MissingBlocks:    newState.MissedBlocks,
	}

	if oldState.MissedBlocks < newState.MissedBlocks {
		// skipping blocks
		g.Logger.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator's missed blocks increasing")
		entry.Direction = INCREASING
		entry.Emoji = newGroup.EmojiStart
		entry.Description = newGroup.DescStart
	} else {
		// restoring
		g.Logger.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator's missed blocks decreasing")
		entry.Direction = DECREASING
		entry.Emoji = newGroup.EmojiEnd
		entry.Description = newGroup.DescEnd
	}

	return entry, true
}

func (g *ReportGenerator) GenerateReport() *Report {
	newState, err := g.GetNewState()
	if err != nil {
		g.Logger.Error().Err(err).Msg("Error getting new state")
		return &Report{}
	}

	if len(g.State) == 0 {
		g.Logger.Info().Msg("No previous state, skipping.")
		g.State = newState
		return &Report{}
	}

	entries := []ReportEntry{}

	for address, info := range newState {
		oldState, ok := g.State[address]
		if !ok {
			g.Logger.Warn().Str("address", address).Msg("No old state present for address")
			continue
		}

		entry, present := g.GetValidatorReportEntry(oldState, info)
		if !present {
			g.Logger.Trace().
				Str("address", address).
				Msg("No report entry present")
			continue
		}

		entries = append(entries, *entry)
	}

	g.State = newState

	return &Report{Entries: entries}
}
