package main

import (
	"time"
)

type Direction int

const (
	INCREASING = iota
	DECREASING
	JAILED
	UNJAILED
	TOMBSTONED
)

const (
	TombstonedEmoji = "💀"
	JailedEmoju     = "❌"
	UnjailedEmoji   = "👌"
)

const (
	TombstonedDesc = "was tombstoned"
	JailedDesc     = "was jailed"
	UnjailedDesc   = "was unjailed"
)

type ValidatorState struct {
	Address          string
	Moniker          string
	ConsensusAddress string
	MissedBlocks     int64
	Jailed           bool
	Tombstoned       bool
}

type ValidatorsState map[string]ValidatorState

type ReportEntry struct {
	ValidatorAddress string
	ValidatorMoniker string
	Emoji            string
	Description      string
	MissingBlocks    int64
	Direction        Direction
}

func (r ReportEntry) GetTimeToJail(params *Params) time.Duration {
	blocksLeftToJail := params.MissedBlocksToJail - r.MissingBlocks
	secondsLeftToJail := params.AvgBlockTime * float64(blocksLeftToJail)

	return time.Duration(secondsLeftToJail) * time.Second
}

type Report struct {
	Entries []ReportEntry
}

type Reporter interface {
	Serialize(Report) string
	Init()
	Enabled() bool
	SendReport(Report) error
	Name() string
}
