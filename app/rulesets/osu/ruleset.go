package osu

import (
	"fmt"
	"github.com/olekukonko/tablewriter"
	"github.com/wieku/danser-go/app/beatmap"
	"github.com/wieku/danser-go/app/beatmap/difficulty"
	"github.com/wieku/danser-go/app/beatmap/objects"
	"github.com/wieku/danser-go/app/graphics"
	"github.com/wieku/danser-go/app/rulesets/osu/performance"
	"github.com/wieku/danser-go/app/settings"
	"github.com/wieku/danser-go/app/utils"
	"github.com/wieku/danser-go/framework/math/mutils"
	"github.com/wieku/danser-go/framework/math/vector"
	"log"
	"math"
	"sort"
	"strings"
)

const Tolerance2B = 3

type Grade int64

const (
	D = Grade(iota)
	C
	B
	A
	S
	SH
	SS
	SSH
	NONE
)

var GradesText = []string{"D", "C", "B", "A", "S", "SH", "SS", "SSH", "None"}

type ClickAction int64

const (
	Ignored = ClickAction(iota)
	Shake
	Click
)

type ComboResult int64

var ComboResults = struct {
	Reset,
	Hold,
	Increase ComboResult
}{0, 1, 2}

type buttonState struct {
	Left, Right bool
}

func (state buttonState) BothReleased() bool {
	return !(state.Left || state.Right)
}

type HitObject interface {
	Init(ruleset *OsuRuleSet, object objects.IHitObject, players []*difficultyPlayer)
	UpdateFor(player *difficultyPlayer, time int64, processSliderEndsAhead bool) bool
	UpdateClickFor(player *difficultyPlayer, time int64) bool
	UpdatePostFor(player *difficultyPlayer, time int64) bool
	UpdatePost(time int64) bool
	IsHit(player *difficultyPlayer) bool
	GetFadeTime() int64
	GetNumber() int64
}

type difficultyPlayer struct {
	cursor          *graphics.Cursor
	diff            *difficulty.Difficulty
	DoubleClick     bool
	alreadyStolen   bool
	buttons         buttonState
	gameDownState   bool
	mouseDownButton Buttons
	lastButton      Buttons
	lastButton2     Buttons
	leftCond        bool
	leftCondE       bool
	rightCond       bool
	rightCondE      bool
}

type scoreProcessor interface {
	Init(beatMap *beatmap.BeatMap, player *difficultyPlayer)
	AddResult(result HitResult, comboResult ComboResult)
	ModifyResult(result HitResult, src HitObject) HitResult
	GetScore() int64
	GetCombo() int64
}

type subSet struct {
	player         *difficultyPlayer
	rawScore       int64
	accuracy       float64
	maxCombo       int64
	numObjects     int64
	grade          Grade
	ppv2           *performance.PPv2
	hits           map[HitResult]int64
	currentKatu    int
	currentBad     int
	hp             *HealthProcessor
	gekiCount      int64
	katuCount      int64
	recoveries     int
	scoreProcessor scoreProcessor
	failed         bool
}

type MapTo struct {
	ncircles int
	nsliders int
	nobjects int
	maxCombo int
}

type hitListener func(cursor *graphics.Cursor, time int64, number int64, position vector.Vector2d, result HitResult, comboResult ComboResult, ppResults performance.PPv2Results, score int64)

type endListener func(time int64, number int64)

type failListener func(cursor *graphics.Cursor)

type OsuRuleSet struct {
	beatMap *beatmap.BeatMap
	cursors map[*graphics.Cursor]*subSet

	ended bool

	mapStats []*MapTo
	oppDiffs map[difficulty.Modifier][]performance.Stars

	queue        []HitObject
	processed    []HitObject
	hitListener  hitListener
	endListener  endListener
	failListener failListener

	experimentalPP bool
}

func NewOsuRuleset(beatMap *beatmap.BeatMap, cursors []*graphics.Cursor, mods []difficulty.Modifier) *OsuRuleSet {
	log.Println("Creating osu! ruleset...")

	ruleset := new(OsuRuleSet)
	ruleset.beatMap = beatMap
	ruleset.oppDiffs = make(map[difficulty.Modifier][]performance.Stars)

	ruleset.mapStats = make([]*MapTo, 0, len(ruleset.beatMap.HitObjects))

	for i, o := range ruleset.beatMap.HitObjects {
		mapTo := &MapTo{}

		if i > 0 {
			*mapTo = *ruleset.mapStats[i-1]
		}

		if s, ok := o.(*objects.Slider); ok {
			mapTo.nsliders++
			mapTo.maxCombo += len(s.ScorePoints)
		} else if _, ok := o.(*objects.Circle); ok {
			mapTo.ncircles++
		}

		mapTo.maxCombo++
		mapTo.nobjects++

		ruleset.mapStats = append(ruleset.mapStats, mapTo)
	}

	if settings.Gameplay.UseLazerPP {
		log.Println("Using pp calc version 2021-10-29 with following changes:")
		log.Println("\t- Total SR now better correlates with PP: https://github.com/ppy/osu/pull/13986")
		log.Println("\t- Added difficulty skill for flashlight, so that FL weighting can be better analysed than the current heuristics: https://github.com/ppy/osu/pull/14217")
		log.Println("\t- Added flashlight skill to total SR: https://github.com/ppy/osu/pull/14753")
		log.Println("\t- Removed speed cap in difficulty calculation: https://github.com/ppy/osu/pull/14617")
		log.Println("\t- Added relax mod PP calculation: https://github.com/ppy/osu/pull/14942")
		log.Println("\t- Rhythm complexity SR rework: https://github.com/ppy/osu/pull/14395")
		log.Println("\t- Fixed instant spinners giving insane amounts of strain: https://github.com/ppy/osu/pull/15009")
		log.Println("\t- Approximation of number of misses + sliderbreaks: https://github.com/ppy/osu/pull/15086")

		ruleset.experimentalPP = true
	} else {
		log.Println("Using pp calc version 2021-07-27: https://osu.ppy.sh/home/news/2021-07-27-performance-points-star-rating-updates")
	}

	ruleset.cursors = make(map[*graphics.Cursor]*subSet)

	diffPlayers := make([]*difficultyPlayer, 0, len(cursors))

	for i, cursor := range cursors {
		diff := difficulty.NewDifficulty(beatMap.Diff.GetHPDrain(), beatMap.Diff.GetCS(), beatMap.Diff.GetOD(), beatMap.Diff.GetAR())
		diff.SetMods(mods[i] | (beatMap.Diff.Mods & difficulty.ScoreV2)) // if beatmap has ScoreV2 mod, force it for all players
		diff.SetCustomSpeed(beatMap.Diff.CustomSpeed)

		player := &difficultyPlayer{cursor: cursor, diff: diff}
		diffPlayers = append(diffPlayers, player)

		if ruleset.oppDiffs[mods[i]&difficulty.DifficultyAdjustMask] == nil {
			ruleset.oppDiffs[mods[i]&difficulty.DifficultyAdjustMask] = performance.CalculateStep(ruleset.beatMap.HitObjects, diff, ruleset.experimentalPP)

			star := ruleset.oppDiffs[mods[i]&difficulty.DifficultyAdjustMask][len(ruleset.oppDiffs[mods[i]&difficulty.DifficultyAdjustMask])-1]
			log.Println("\tAim Stars:  ", star.Aim)
			log.Println("\tSpeed Stars:", star.Speed)

			if ruleset.experimentalPP && mods[i].Active(difficulty.Flashlight) {
				log.Println("\tFL Stars:   ", star.Flashlight)
			}

			log.Println("\tTotal Stars:", star.Total)
		}

		log.Println(fmt.Sprintf("Calculating HP rates for \"%s\"...", cursor.Name))

		hp := NewHealthProcessor(beatMap, diff, !cursor.OldSpinnerScoring)
		hp.CalculateRate()
		hp.ResetHp()

		log.Println("\tPassive drain rate:", hp.PassiveDrain/2*1000)
		log.Println("\tNormal multiplier:", hp.HpMultiplierNormal)
		log.Println("\tCombo end multiplier:", hp.HpMultiplierComboEnd)

		recoveries := 0
		if diff.CheckModActive(difficulty.Easy) {
			recoveries = 2
		}

		hp.AddFailListener(func() {
			ruleset.failInternal(player)
		})

		var sc scoreProcessor

		if diff.CheckModActive(difficulty.ScoreV2) {
			sc = newScoreV2Processor()
		} else {
			sc = newScoreV1Processor()
		}

		sc.Init(beatMap, player)

		ruleset.cursors[cursor] = &subSet{
			player:         player,
			accuracy:       100,
			grade:          NONE,
			ppv2:           &performance.PPv2{},
			hits:           make(map[HitResult]int64),
			hp:             hp,
			recoveries:     recoveries,
			scoreProcessor: sc,
		}
	}

	for _, obj := range beatMap.HitObjects {
		if circle, ok := obj.(*objects.Circle); ok {
			rCircle := new(Circle)
			rCircle.Init(ruleset, circle, diffPlayers)
			ruleset.queue = append(ruleset.queue, rCircle)
		}

		if slider, ok := obj.(*objects.Slider); ok {
			rSlider := new(Slider)
			rSlider.Init(ruleset, slider, diffPlayers)
			ruleset.queue = append(ruleset.queue, rSlider)
		}

		if spinner, ok := obj.(*objects.Spinner); ok {
			rSpinner := new(Spinner)
			rSpinner.Init(ruleset, spinner, diffPlayers)
			ruleset.queue = append(ruleset.queue, rSpinner)
		}
	}

	return ruleset
}

func (set *OsuRuleSet) Update(time int64) {
	if len(set.processed) > 0 {
		for i := 0; i < len(set.processed); i++ {
			g := set.processed[i]

			if isDone := g.UpdatePost(time); isDone {
				if set.endListener != nil {
					set.endListener(time, g.GetNumber())
				}

				set.processed = append(set.processed[:i], set.processed[i+1:]...)

				i--
			}
		}
	}

	if len(set.queue) > 0 {
		for i := 0; i < len(set.queue); i++ {
			g := set.queue[i]
			if g.GetFadeTime() > time {
				break
			}

			set.processed = append(set.processed, g)

			set.queue = append(set.queue[:i], set.queue[i+1:]...)

			i--
		}
	}

	for _, subSet := range set.cursors {
		subSet.hp.Update(time)
	}

	if len(set.queue) == 0 && len(set.processed) == 0 && !set.ended {
		cs := make([]*graphics.Cursor, 0)
		for c := range set.cursors {
			cs = append(cs, c)
		}

		sort.Slice(cs, func(i, j int) bool {
			return set.cursors[cs[i]].scoreProcessor.GetScore() > set.cursors[cs[j]].scoreProcessor.GetScore()
		})

		tableString := &strings.Builder{}
		table := tablewriter.NewWriter(tableString)
		table.SetHeader([]string{"#", "Player", "Score", "Accuracy", "Grade", "300", "100", "50", "Miss", "Combo", "Max Combo", "Mods", "PP"})

		for i, c := range cs {
			var data []string
			data = append(data, fmt.Sprintf("%d", i+1))
			data = append(data, c.Name)
			data = append(data, utils.Humanize(set.cursors[c].scoreProcessor.GetScore()))
			data = append(data, fmt.Sprintf("%.2f", set.cursors[c].accuracy))
			data = append(data, GradesText[set.cursors[c].grade])
			data = append(data, utils.Humanize(set.cursors[c].hits[Hit300]))
			data = append(data, utils.Humanize(set.cursors[c].hits[Hit100]))
			data = append(data, utils.Humanize(set.cursors[c].hits[Hit50]))
			data = append(data, utils.Humanize(set.cursors[c].hits[Miss]))
			data = append(data, utils.Humanize(set.cursors[c].scoreProcessor.GetCombo()))
			data = append(data, utils.Humanize(set.cursors[c].maxCombo))
			data = append(data, set.cursors[c].player.diff.Mods.String())
			data = append(data, fmt.Sprintf("%.2f", set.cursors[c].ppv2.Results.Total))
			table.Append(data)
		}

		table.Render()

		for _, s := range strings.Split(tableString.String(), "\n") {
			log.Println(s)
		}

		set.ended = true
	}
}

func (set *OsuRuleSet) UpdateClickFor(cursor *graphics.Cursor, time int64) {
	player := set.cursors[cursor].player

	player.alreadyStolen = false

	if player.cursor.IsReplayFrame || player.cursor.IsPlayer {
		player.leftCond = !player.buttons.Left && player.cursor.LeftButton
		player.rightCond = !player.buttons.Right && player.cursor.RightButton

		player.leftCondE = player.leftCond
		player.rightCondE = player.rightCond

		if player.buttons.Left != player.cursor.LeftButton || player.buttons.Right != player.cursor.RightButton {
			player.gameDownState = player.cursor.LeftButton || player.cursor.RightButton
			player.lastButton2 = player.lastButton
			player.lastButton = player.mouseDownButton

			player.mouseDownButton = Buttons(0)

			if player.cursor.LeftButton {
				player.mouseDownButton |= Left
			}

			if player.cursor.RightButton {
				player.mouseDownButton |= Right
			}
		}
	}

	if len(set.processed) > 0 {
		for i := 0; i < len(set.processed); i++ {
			g := set.processed[i]

			g.UpdateClickFor(player, time)
		}
	}

	if player.cursor.IsReplayFrame || player.cursor.IsPlayer {
		player.buttons.Left = player.cursor.LeftButton
		player.buttons.Right = player.cursor.RightButton
	}
}

func (set *OsuRuleSet) UpdateNormalFor(cursor *graphics.Cursor, time int64, processSliderEndsAhead bool) {
	player := set.cursors[cursor].player

	wasSliderAlready := false

	if len(set.processed) > 0 {
		for i := 0; i < len(set.processed); i++ {
			g := set.processed[i]

			s, isSlider := g.(*Slider)

			if isSlider {
				if wasSliderAlready {
					continue
				}

				if !s.IsHit(player) {
					wasSliderAlready = true
				}
			}

			g.UpdateFor(player, time, processSliderEndsAhead)
		}
	}
}

func (set *OsuRuleSet) UpdatePostFor(cursor *graphics.Cursor, time int64) {
	player := set.cursors[cursor].player

	if len(set.processed) > 0 {
		for i := 0; i < len(set.processed); i++ {
			g := set.processed[i]

			g.UpdatePostFor(player, time)
		}
	}
}

func (set *OsuRuleSet) SendResult(time int64, cursor *graphics.Cursor, src HitObject, x, y float32, result HitResult, comboResult ComboResult) {
	number := src.GetNumber()
	subSet := set.cursors[cursor]

	if result == Ignore || result == PositionalMiss {
		if result == PositionalMiss && set.hitListener != nil && !subSet.player.diff.Mods.Active(difficulty.Relax) {
			set.hitListener(cursor, time, number, vector.NewVec2f(x, y).Copy64(), result, comboResult, subSet.ppv2.Results, subSet.scoreProcessor.GetScore())
		}

		return
	}

	result = subSet.scoreProcessor.ModifyResult(result, src)
	subSet.scoreProcessor.AddResult(result, comboResult)

	if result&BaseHitsM > 0 {
		subSet.rawScore += result.ScoreValue()
		subSet.hits[result]++
		subSet.numObjects++
	}

	subSet.maxCombo = mutils.MaxI64(subSet.scoreProcessor.GetCombo(), subSet.maxCombo)

	if subSet.numObjects == 0 {
		subSet.accuracy = 100
	} else {
		subSet.accuracy = 100 * float64(subSet.rawScore) / float64(subSet.numObjects*300)
	}

	ratio := float64(subSet.hits[Hit300]) / float64(subSet.numObjects)

	if subSet.hits[Hit300] == subSet.numObjects {
		if subSet.player.diff.Mods&(difficulty.Hidden|difficulty.Flashlight) > 0 {
			subSet.grade = SSH
		} else {
			subSet.grade = SS
		}
	} else if ratio > 0.9 && float64(subSet.hits[Hit50])/float64(subSet.numObjects) < 0.01 && subSet.hits[Miss] == 0 {
		if subSet.player.diff.Mods&(difficulty.Hidden|difficulty.Flashlight) > 0 {
			subSet.grade = SH
		} else {
			subSet.grade = S
		}
	} else if ratio > 0.8 && subSet.hits[Miss] == 0 || ratio > 0.9 {
		subSet.grade = A
	} else if ratio > 0.7 && subSet.hits[Miss] == 0 || ratio > 0.8 {
		subSet.grade = B
	} else if ratio > 0.6 {
		subSet.grade = C
	} else {
		subSet.grade = D
	}

	index := mutils.MaxI64(0, subSet.numObjects-1)

	mapTo := set.mapStats[index]
	diff := set.oppDiffs[subSet.player.diff.Mods&difficulty.DifficultyAdjustMask][index]

	subSet.ppv2.PPv2x(diff, set.experimentalPP, mapTo.maxCombo, mapTo.nsliders, mapTo.ncircles, mapTo.nobjects, int(subSet.maxCombo), int(subSet.hits[Hit300]), int(subSet.hits[Hit100]), int(subSet.hits[Hit50]), int(subSet.hits[Miss]), subSet.player.diff)

	switch result {
	case Hit100:
		subSet.currentKatu++
	case Hit50, Miss:
		subSet.currentBad++
	}

	if result&BaseHitsM > 0 && (int(number) == len(set.beatMap.HitObjects)-1 || (int(number) < len(set.beatMap.HitObjects)-1 && set.beatMap.HitObjects[number+1].IsNewCombo())) {
		allClicked := true

		// We don't want to give geki/katu if all objects in current combo weren't clicked
		index := sort.Search(len(set.processed), func(i int) bool {
			return set.processed[i].GetNumber() >= number
		})

		for i := index - 1; i >= 0; i-- {
			obj := set.processed[i]

			if !obj.IsHit(subSet.player) {
				allClicked = false
				break
			}

			if set.beatMap.HitObjects[obj.GetNumber()].IsNewCombo() {
				break
			}
		}

		if result&BaseHits > 0 {
			if subSet.currentKatu == 0 && subSet.currentBad == 0 && allClicked {
				result |= GekiAddition
				subSet.gekiCount++
			} else if subSet.currentBad == 0 && allClicked {
				result |= KatuAddition
				subSet.katuCount++
			} else {
				result |= MuAddition
			}
		}

		subSet.currentBad = 0
		subSet.currentKatu = 0
	}

	subSet.hp.AddResult(result)

	if set.hitListener != nil {
		set.hitListener(cursor, time, number, vector.NewVec2f(x, y).Copy64(), result, comboResult, subSet.ppv2.Results, subSet.scoreProcessor.GetScore())
	}

	if len(set.cursors) == 1 && !settings.RECORD {
		log.Println(fmt.Sprintf(
			"Got: %3d, Combo: %4d, Max Combo: %4d, Score: %9d, Acc: %6.2f%%, 300: %4d, 100: %3d, 50: %2d, miss: %2d, from: %d, at: %d, pos: %.0fx%.0f, pp: %.2f",
			result.ScoreValue(),
			subSet.scoreProcessor.GetCombo(),
			subSet.maxCombo,
			subSet.scoreProcessor.GetScore(),
			subSet.accuracy,
			subSet.hits[Hit300],
			subSet.hits[Hit100],
			subSet.hits[Hit50],
			subSet.hits[Miss],
			number,
			time,
			x,
			y,
			subSet.ppv2.Results.Total,
		))
	}
}

func (set *OsuRuleSet) CanBeHit(time int64, object HitObject, player *difficultyPlayer) ClickAction {
	if _, ok := object.(*Circle); ok {
		index := -1

		for i, g := range set.processed {
			if g == object {
				index = i
			}
		}

		if index > 0 && set.beatMap.HitObjects[set.processed[index-1].GetNumber()].GetStackIndex(player.diff.Mods) > 0 && !set.processed[index-1].IsHit(player) {
			return Ignored //don't shake the stacks
		}
	}

	for _, g := range set.processed {
		if !g.IsHit(player) {
			if g.GetNumber() != object.GetNumber() {
				if set.beatMap.HitObjects[g.GetNumber()].GetEndTime()+Tolerance2B < set.beatMap.HitObjects[object.GetNumber()].GetStartTime() {
					return Shake
				}
			} else {
				break
			}
		}
	}

	hitRange := difficulty.HittableRange
	if player.diff.CheckModActive(difficulty.Relax2) {
		hitRange -= 200
	}

	if math.Abs(float64(time-int64(set.beatMap.HitObjects[object.GetNumber()].GetStartTime()))) >= hitRange {
		return Shake
	}

	return Click
}

func (set *OsuRuleSet) failInternal(player *difficultyPlayer) {
	subSet := set.cursors[player.cursor]

	if player.diff.CheckModActive(difficulty.NoFail | difficulty.Relax | difficulty.Relax2) {
		return
	}

	// EZ mod gives 2 additional lives
	if subSet.recoveries > 0 {
		subSet.hp.Increase(160, false)
		subSet.recoveries--

		return
	}

	// actual fail
	if set.failListener != nil && !subSet.failed {
		set.failListener(player.cursor)
	}

	subSet.failed = true
}

func (set *OsuRuleSet) SetListener(listener hitListener) {
	set.hitListener = listener
}

func (set *OsuRuleSet) SetEndListener(listener endListener) {
	set.endListener = listener
}

func (set *OsuRuleSet) SetFailListener(listener failListener) {
	set.failListener = listener
}

func (set *OsuRuleSet) GetResults(cursor *graphics.Cursor) (float64, int64, int64, Grade) {
	subSet := set.cursors[cursor]
	return subSet.accuracy, subSet.maxCombo, subSet.scoreProcessor.GetScore(), subSet.grade
}

func (set *OsuRuleSet) GetHits(cursor *graphics.Cursor) (int64, int64, int64, int64, int64, int64) {
	subSet := set.cursors[cursor]
	return subSet.hits[Hit300], subSet.hits[Hit100], subSet.hits[Hit50], subSet.hits[Miss], subSet.gekiCount, subSet.katuCount
}

func (set *OsuRuleSet) GetHP(cursor *graphics.Cursor) float64 {
	subSet := set.cursors[cursor]
	return subSet.hp.Health / MaxHp
}

func (set *OsuRuleSet) GetPP(cursor *graphics.Cursor) performance.PPv2Results {
	subSet := set.cursors[cursor]
	return subSet.ppv2.Results
}

func (set *OsuRuleSet) IsPerfect(cursor *graphics.Cursor) bool {
	subSet := set.cursors[cursor]
	return subSet.maxCombo == int64(set.mapStats[subSet.numObjects-1].maxCombo)
}

func (set *OsuRuleSet) GetPlayer(cursor *graphics.Cursor) *difficultyPlayer {
	subSet := set.cursors[cursor]
	return subSet.player
}

func (set *OsuRuleSet) GetProcessed() []HitObject {
	return set.processed
}

func (set *OsuRuleSet) GetBeatMap() *beatmap.BeatMap {
	return set.beatMap
}
