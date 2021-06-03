package main

import "time"

type Direction int

const (
	START_MISSING_BLOCKS Direction = iota
	MISSING_BLOCKS
	STOPPED_MISSING_BLOCKS
	WENT_BACK_TO_NORMAL
	JAILED
)

type ReportEntry struct {
	ValidatorAddress    string
	ValidatorMoniker    string
	Pubkey              string
	Direction           Direction
	BeforeBlocksMissing int64
	NowBlocksMissing    int64
}

func (r ReportEntry) GetTimeToJail() time.Duration {
	blocksLeftToJail := MissedBlocksToJail - r.NowBlocksMissing
	secondsLeftToJail := AvgBlockTime * float64(blocksLeftToJail)

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
