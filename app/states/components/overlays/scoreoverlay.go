package overlays

import (
	"fmt"
	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/wieku/danser-go/app/audio"
	"github.com/wieku/danser-go/app/beatmap"
	"github.com/wieku/danser-go/app/beatmap/difficulty"
	"github.com/wieku/danser-go/app/beatmap/objects"
	camera2 "github.com/wieku/danser-go/app/bmath/camera"
	"github.com/wieku/danser-go/app/discord"
	"github.com/wieku/danser-go/app/graphics"
	"github.com/wieku/danser-go/app/input"
	"github.com/wieku/danser-go/app/rulesets/osu"
	"github.com/wieku/danser-go/app/rulesets/osu/performance"
	"github.com/wieku/danser-go/app/settings"
	"github.com/wieku/danser-go/app/skin"
	"github.com/wieku/danser-go/app/states/components/common"
	"github.com/wieku/danser-go/app/states/components/overlays/play"
	"github.com/wieku/danser-go/framework/assets"
	"github.com/wieku/danser-go/framework/bass"
	"github.com/wieku/danser-go/framework/graphics/batch"
	"github.com/wieku/danser-go/framework/graphics/font"
	"github.com/wieku/danser-go/framework/graphics/shape"
	"github.com/wieku/danser-go/framework/graphics/sprite"
	"github.com/wieku/danser-go/framework/graphics/texture"
	"github.com/wieku/danser-go/framework/math/animation"
	"github.com/wieku/danser-go/framework/math/animation/easing"
	color2 "github.com/wieku/danser-go/framework/math/color"
	"github.com/wieku/danser-go/framework/math/mutils"
	"github.com/wieku/danser-go/framework/math/vector"
	"math"
	"strconv"
	"strings"
)

const (
	vAccOffset   = 4.8
	barWidth     = 272.0
	barThickness = 4.8
	minBlinks    = 5
	blinkTime    = 200
)

type Overlay interface {
	Update(float64)
	SetMusic(bass.ITrack)
	DrawBackground(batch *batch.QuadBatch, colors []color2.Color, alpha float64)
	DrawBeforeObjects(batch *batch.QuadBatch, colors []color2.Color, alpha float64)
	DrawNormal(batch *batch.QuadBatch, colors []color2.Color, alpha float64)
	DrawHUD(batch *batch.QuadBatch, colors []color2.Color, alpha float64)
	IsBroken(cursor *graphics.Cursor) bool
	DisableAudioSubmission(b bool)
	ShouldDrawHUDBeforeCursor() bool
}

type ScoreOverlay struct {
	lastTime float64
	combo    int64
	newCombo int64

	comboSlide     *animation.Glider
	newComboScale  *animation.Glider
	newComboScaleB *animation.Glider
	newComboFadeB  *animation.Glider

	scoreGlider    *animation.TargetGlider
	accuracyGlider *animation.TargetGlider

	ruleset    *osu.OsuRuleSet
	cursor     *graphics.Cursor
	combobreak *bass.Sample
	music      bass.ITrack
	nextEnd    float64
	results    *play.HitResults

	keyStates   [4]bool
	keyCounters [4]int
	lastPresses [4]float64
	keyOverlay  *sprite.Manager
	keys        []*sprite.Sprite

	ScaledWidth  float64
	ScaledHeight float64
	camera       *camera2.Camera

	keyFont    *font.Font
	scoreFont  *font.Font
	comboFont  *font.Font
	scoreEFont *font.Font

	bgDim *animation.Glider

	hitErrorMeter *play.HitErrorMeter

	aimErrorMeter *play.AimErrorMeter

	skip *sprite.Animation

	shapeRenderer *shape.Renderer

	boundaries *common.Boundaries

	mods       *sprite.Manager
	notFirst   bool
	flashlight *common.Flashlight
	delta      float64

	entry         *play.ScoreBoard
	audioTime     float64
	normalTime    float64
	breakMode     bool
	currentBreak  *beatmap.Pause
	sPass         *sprite.Sprite
	sFail         *sprite.Sprite
	passContainer *sprite.Manager

	rankBack  *sprite.Sprite
	rankFront *sprite.Sprite

	oldGrade osu.Grade

	hpBar *play.HpBar

	arrows *sprite.Manager

	resultsFade *animation.Glider
	hpSections  []vector.Vector2d
	panel       *play.RankingPanel
	created     bool
	skipTo      float64

	audioDisabled bool
	beatmapEnd    float64

	circularMetre *texture.TextureRegion

	hitCounts *play.HitDisplay

	ppDisplay *play.PPDisplay
}

func loadFonts() {
	if font.GetFont("Ubuntu Regular") == nil {
		file, _ := assets.Open("assets/fonts/Ubuntu-Regular.ttf")
		font.LoadFont(file)
		file.Close()
	}

	if font.GetFont("Quicksand Bold") == nil {
		file, _ := assets.Open("assets/fonts/Quicksand-Bold.ttf")
		font.LoadFont(file)
		file.Close()
	}
}

func NewScoreOverlay(ruleset *osu.OsuRuleSet, cursor *graphics.Cursor) *ScoreOverlay {
	loadFonts()

	overlay := new(ScoreOverlay)

	overlay.beatmapEnd = math.Inf(1)
	overlay.oldGrade = -10

	overlay.ScaledHeight = 768
	overlay.ScaledWidth = settings.Graphics.GetAspectRatio() * overlay.ScaledHeight

	overlay.results = play.NewHitResults(ruleset.GetBeatMap().Diff)
	overlay.ruleset = ruleset
	overlay.cursor = cursor

	overlay.comboSlide = animation.NewGlider(0)
	overlay.comboSlide.SetEasing(easing.OutQuad)

	overlay.newComboScale = animation.NewGlider(1.28)
	overlay.newComboScaleB = animation.NewGlider(1.28)
	overlay.newComboFadeB = animation.NewGlider(0)

	overlay.scoreGlider = animation.NewTargetGlider(0, 0)
	overlay.accuracyGlider = animation.NewTargetGlider(0, 2)

	overlay.ppDisplay = play.NewPPDisplay(ruleset.GetBeatMap().Diff.Mods, settings.Gameplay.UseLazerPP)

	overlay.resultsFade = animation.NewGlider(0)

	overlay.bgDim = animation.NewGlider(1)

	overlay.combobreak = audio.LoadSample("combobreak")

	audio.LoadSample("sectionpass")
	audio.LoadSample("sectionfail")

	overlay.sPass = sprite.NewSpriteSingle(skin.GetTexture("section-pass"), 0, vector.NewVec2d(overlay.ScaledWidth, overlay.ScaledHeight).Scl(0.5), vector.Centre)
	overlay.sPass.SetAlpha(0)

	overlay.sFail = sprite.NewSpriteSingle(skin.GetTexture("section-fail"), 0, vector.NewVec2d(overlay.ScaledWidth, overlay.ScaledHeight).Scl(0.5), vector.Centre)
	overlay.sFail.SetAlpha(0)

	overlay.rankBack = sprite.NewSpriteSingle(nil, 0, vector.NewVec2d(0, 0), vector.Centre)
	overlay.rankBack.SetAlpha(0)

	overlay.rankFront = sprite.NewSpriteSingle(nil, 0, vector.NewVec2d(0, 0), vector.Centre)
	overlay.rankFront.SetAlpha(0)

	overlay.passContainer = sprite.NewManager()
	overlay.passContainer.Add(overlay.sPass)
	overlay.passContainer.Add(overlay.sFail)

	discord.UpdatePlay(cursor)

	overlay.keyFont = font.GetFont("Quicksand Bold")
	overlay.scoreEFont = skin.GetFont("scoreentry")
	overlay.scoreFont = skin.GetFont("score")
	overlay.comboFont = skin.GetFont("combo")
	overlay.circularMetre = skin.GetTextureSource("circularmetre", skin.LOCAL)

	ruleset.SetListener(overlay.hitReceived)

	overlay.camera = camera2.NewCamera()
	overlay.camera.SetViewportF(0, int(overlay.ScaledHeight), int(overlay.ScaledWidth), 0)
	overlay.camera.Update()

	overlay.keyOverlay = sprite.NewManager()

	keyBg := sprite.NewSpriteSingle(skin.GetTexture("inputoverlay-background"), 0, vector.NewVec2d(overlay.ScaledWidth, overlay.ScaledHeight/2-64), vector.TopLeft)
	keyBg.SetScaleV(vector.NewVec2d(1.05, 1))
	keyBg.ShowForever(true)
	keyBg.SetRotation(math.Pi / 2)

	overlay.keyOverlay.Add(keyBg)

	for i := 0; i < 4; i++ {
		posY := overlay.ScaledHeight/2 - 64 + (30.4+float64(i)*47.2)*settings.Gameplay.KeyOverlay.Scale

		key := sprite.NewSpriteSingle(skin.GetTexture("inputoverlay-key"), 1, vector.NewVec2d(overlay.ScaledWidth-24*settings.Gameplay.KeyOverlay.Scale, posY), vector.Centre)
		key.ShowForever(true)

		overlay.keys = append(overlay.keys, key)
		overlay.keyOverlay.Add(key)
	}

	overlay.hitErrorMeter = play.NewHitErrorMeter(overlay.ScaledWidth, overlay.ScaledHeight, ruleset.GetBeatMap().Diff)

	overlay.aimErrorMeter = play.NewAimErrorMeter(ruleset.GetBeatMap().Diff)

	showAfterSkip := 2000.0

	beatLen := overlay.ruleset.GetBeatMap().Timings.GetPointAt(0).GetBaseBeatLength()
	if beatLen > 0 {
		showAfterSkip = beatLen
		if beatLen < 500 {
			showAfterSkip *= 8
		} else {
			showAfterSkip *= 4
		}
	}

	overlay.skipTo = overlay.ruleset.GetBeatMap().HitObjects[0].GetStartTime() - showAfterSkip

	if !settings.SKIP && overlay.skipTo > 1200+math.Min(1800, overlay.ruleset.GetBeatMap().Diff.Preempt) {
		skipFrames := skin.GetFrames("play-skip", true)
		overlay.skip = sprite.NewAnimation(skipFrames, skin.GetInfo().GetFrameTime(len(skipFrames)), true, 0.0, vector.NewVec2d(overlay.ScaledWidth, overlay.ScaledHeight), vector.BottomRight)
		overlay.skip.SetAlpha(0.0)
		overlay.skip.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, 0, 400, 0.0, 0.6))
		overlay.skip.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, overlay.skipTo, overlay.skipTo+200, 0.6, 0.0))
	}

	overlay.hpBar = play.NewHpBar()

	overlay.hitCounts = play.NewHitDisplay(overlay.ruleset, overlay.cursor, overlay.keyFont)

	overlay.shapeRenderer = shape.NewRenderer()

	overlay.boundaries = common.NewBoundaries()

	overlay.mods = sprite.NewManager()

	if overlay.ruleset.GetBeatMap().Diff.Mods.Active(difficulty.Flashlight) {
		overlay.flashlight = common.NewFlashlight(overlay.ruleset.GetBeatMap())
	}

	overlay.entry = play.NewScoreboard(overlay.ruleset.GetBeatMap(), overlay.cursor.ScoreID)
	overlay.entry.AddPlayer(overlay.cursor.Name, overlay.cursor.IsAutoplay)

	overlay.initArrows()

	return overlay
}

func (overlay *ScoreOverlay) hitReceived(c *graphics.Cursor, time int64, number int64, position vector.Vector2d, result osu.HitResult, comboResult osu.ComboResult, ppResults performance.PPv2Results, _ int64) {
	object := overlay.ruleset.GetBeatMap().HitObjects[number]

	if result&(osu.BaseHitsM) > 0 {
		overlay.results.AddResult(time, result, position, object)
	}

	_, hC := object.(*objects.Circle)
	allowCircle := hC && (result&(osu.BaseHits|osu.PositionalMiss) > 0)
	_, sl := object.(*objects.Slider)
	allowSlider := sl && (result&(osu.SliderStart|osu.PositionalMiss)) > 0

	if allowCircle || allowSlider {
		timeDiff := float64(time) - object.GetStartTime()

		overlay.hitErrorMeter.Add(float64(time), timeDiff, result == osu.PositionalMiss)

		var startPos *vector.Vector2f
		if number > 0 {
			pos := overlay.ruleset.GetBeatMap().HitObjects[number-1].GetStackedEndPositionMod(overlay.ruleset.GetBeatMap().Diff.Mods)
			startPos = &pos
		}

		endPos := object.GetStackedStartPositionMod(overlay.ruleset.GetBeatMap().Diff.Mods)

		overlay.aimErrorMeter.Add(float64(time), c.Position, startPos, &endPos)
	}

	if result == osu.PositionalMiss {
		return
	}

	if comboResult == osu.ComboResults.Increase {
		overlay.newComboScaleB.Reset()
		overlay.newComboScaleB.AddEventS(overlay.normalTime, overlay.normalTime+300, 2, 1.28)

		overlay.newComboFadeB.Reset()
		overlay.newComboFadeB.AddEventS(overlay.normalTime, overlay.normalTime+300, 0.6, 0.0)

		overlay.animate(overlay.normalTime)

		overlay.combo = overlay.newCombo
		overlay.newCombo++
		overlay.nextEnd = overlay.normalTime + 300
	} else if comboResult == osu.ComboResults.Reset {
		if overlay.newCombo > 20 && overlay.combobreak != nil && !overlay.audioDisabled {
			overlay.combobreak.Play()
		}
		overlay.newCombo = 0
	}

	if overlay.flashlight != nil {
		overlay.flashlight.UpdateCombo(overlay.newCombo)
	}

	accuracy, mCombo, score, grade := overlay.ruleset.GetResults(overlay.cursor)

	overlay.entry.UpdatePlayer(score, mCombo)

	overlay.scoreGlider.SetTarget(float64(score))
	overlay.accuracyGlider.SetTarget(accuracy)

	overlay.ppDisplay.Add(ppResults)

	overlay.hpSections = append(overlay.hpSections, vector.NewVec2d(float64(time), overlay.ruleset.GetHP(overlay.cursor)))

	if overlay.oldGrade != grade {
		go func() {
			gText := strings.ToLower(strings.ReplaceAll(osu.GradesText[grade], "SS", "X"))

			text := skin.GetTexture("ranking-" + gText + "-small")

			overlay.rankBack.Texture = text
			overlay.rankFront.Texture = text

			overlay.oldGrade = grade
		}()
	}
}

func (overlay *ScoreOverlay) animate(time float64) {
	overlay.newComboScale.Reset()
	overlay.newComboScale.AddEventSEase(time, time+50, 1.28, 1.4, easing.InQuad)
	overlay.newComboScale.AddEventSEase(time+50, time+100, 1.4, 1.28, easing.OutQuad)
}

func (overlay *ScoreOverlay) Update(time float64) {
	if overlay.audioTime == 0 {
		overlay.audioTime = time
		overlay.normalTime = time
	}

	delta := time - overlay.audioTime

	if overlay.music != nil && overlay.music.GetState() == bass.MusicPlaying {
		delta /= overlay.music.GetTempo()
	}

	overlay.normalTime += delta

	overlay.audioTime = time

	if !overlay.notFirst && time > -settings.Playfield.LeadInHold*1000 {
		overlay.notFirst = true

		overlay.initMods()
	}

	if input.Win.GetKey(glfw.KeySpace) == glfw.Press {
		if overlay.skip != nil && overlay.music != nil && overlay.music.GetState() == bass.MusicPlaying {
			if overlay.audioTime < overlay.skipTo {
				overlay.music.SetPosition(overlay.skipTo / 1000)
			}
		}
	}

	overlay.results.Update(time)
	overlay.hitErrorMeter.Update(time)
	overlay.aimErrorMeter.Update(time)

	if overlay.skip != nil {
		overlay.skip.Update(time)
	}

	overlay.passContainer.Update(overlay.audioTime)
	overlay.rankBack.Update(overlay.audioTime)
	overlay.rankFront.Update(overlay.audioTime)
	overlay.arrows.Update(overlay.audioTime)

	//normal timing
	overlay.updateNormal(overlay.normalTime)
}

func (overlay *ScoreOverlay) updateNormal(time float64) {
	overlay.updateBreaks(time)

	if overlay.panel != nil {
		overlay.panel.Update(time)
	} else if settings.Gameplay.ShowResultsScreen && !overlay.created && overlay.audioTime >= overlay.beatmapEnd {
		overlay.created = true
		cTime := overlay.normalTime

		createPanel := func() {
			overlay.panel = play.NewRankingPanel(overlay.cursor, overlay.ruleset, overlay.hitErrorMeter, overlay.hpSections)

			s := cTime

			resultsTime := settings.Gameplay.ResultsScreenTime * 1000

			overlay.resultsFade.AddEventS(s, s+500, 0, 1)
			overlay.resultsFade.AddEventS(s+resultsTime+500, s+resultsTime+1000, 1, 0)
		}

		if settings.RECORD {
			createPanel()
		} else {
			go createPanel()
		}
	}

	if overlay.flashlight != nil && time >= 0 {
		overlay.flashlight.Update(time)
		overlay.flashlight.UpdatePosition(overlay.cursor.Position)

		proc := overlay.ruleset.GetProcessed()

		sliding := false
		for _, p := range proc {
			if o, ok := p.(*osu.Slider); ok {
				sliding = sliding || o.IsSliding(overlay.ruleset.GetPlayer(overlay.cursor))
			}
		}

		overlay.flashlight.SetSliding(sliding)
	}

	overlay.mods.Update(time)

	overlay.newComboScale.Update(time)
	overlay.newComboScaleB.Update(time)
	overlay.newComboFadeB.Update(time)
	overlay.comboSlide.Update(time)

	overlay.hpBar.SetHp(overlay.ruleset.GetHP(overlay.cursor))
	overlay.hpBar.Update(time)

	overlay.entry.Update(time)

	overlay.delta += time - overlay.lastTime
	if overlay.delta >= 16.6667 {
		overlay.delta -= 16.6667
		if overlay.combo > overlay.newCombo && overlay.newCombo == 0 {
			overlay.combo--
		}
	}

	if overlay.combo != overlay.newCombo && overlay.nextEnd < time+140 {
		overlay.animate(time)
		overlay.combo = overlay.newCombo
		overlay.nextEnd = math.MaxInt64
	}

	overlay.scoreGlider.Update(time)
	overlay.accuracyGlider.Update(time)
	overlay.ppDisplay.Update(time)
	overlay.hitCounts.Update(time)

	currentStates := [4]bool{overlay.cursor.LeftKey, overlay.cursor.RightKey, overlay.cursor.LeftMouse && !overlay.cursor.LeftKey, overlay.cursor.RightMouse && !overlay.cursor.RightKey}

	for i, state := range currentStates {
		color := color2.Color{R: 1.0, G: 222.0 / 255, B: 0, A: 0}
		if i > 1 {
			color = color2.Color{R: 248.0 / 255, G: 0, B: 158.0 / 255, A: 0}
		}

		if !overlay.keyStates[i] && state {
			key := overlay.keys[i]

			key.ClearTransformationsOfType(animation.Scale)
			key.AddTransform(animation.NewSingleTransform(animation.Scale, easing.OutQuad, time, time+100, 1.0, 0.8))
			key.AddTransform(animation.NewColorTransform(animation.Color3, easing.OutQuad, time, time+100, color2.Color{R: 1, G: 1, B: 1, A: 1}, color))

			overlay.lastPresses[i] = time + 100

			if overlay.isDrain() {
				overlay.keyCounters[i]++
			}
		}

		if overlay.keyStates[i] && !state {
			key := overlay.keys[i]
			key.ClearTransformationsOfType(animation.Scale)
			key.AddTransform(animation.NewSingleTransform(animation.Scale, easing.OutQuad, math.Max(time, overlay.lastPresses[i]), time+100, key.GetScale().Y, 1.0))

			key.AddTransform(animation.NewColorTransform(animation.Color3, easing.OutQuad, time, time+100, color, color2.Color{R: 1, G: 1, B: 1, A: 1}))
		}

		overlay.keyStates[i] = state
	}

	overlay.keyOverlay.Update(time)
	overlay.bgDim.Update(time)

	overlay.resultsFade.Update(time)

	overlay.lastTime = time
}

func (overlay *ScoreOverlay) updateBreaks(time float64) {
	inBreak := false

	for _, b := range overlay.ruleset.GetBeatMap().Pauses {
		if overlay.audioTime < b.GetStartTime() {
			break
		}

		if b.GetEndTime()-b.GetStartTime() >= 1000 && overlay.audioTime >= b.GetStartTime() && overlay.audioTime <= b.GetEndTime() {
			inBreak = true
			overlay.currentBreak = b

			break
		}
	}

	if !overlay.breakMode && inBreak {
		overlay.showPassInfo()

		overlay.comboSlide.AddEvent(time, time+500, -1)
		overlay.bgDim.AddEvent(time, time+500, 0)
		overlay.hpBar.SlideOut()
	} else if overlay.breakMode && !inBreak {
		overlay.comboSlide.AddEvent(time, time+500, 0)
		overlay.bgDim.AddEvent(time, time+500, 1)
		overlay.hpBar.SlideIn()
	}

	overlay.breakMode = inBreak
}

func (overlay *ScoreOverlay) SetMusic(music bass.ITrack) {
	overlay.music = music
}

func (overlay *ScoreOverlay) DrawBackground(batch *batch.QuadBatch, c []color2.Color, alpha float64) {
	overlay.boundaries.Draw(batch.Projection, float32(overlay.ruleset.GetBeatMap().Diff.CircleRadius), float32(alpha*overlay.bgDim.GetValue()))
}

func (overlay *ScoreOverlay) DrawBeforeObjects(batch *batch.QuadBatch, c []color2.Color, alpha float64) {
	overlay.results.DrawBottom(batch, c, 1.0)
}

func (overlay *ScoreOverlay) DrawNormal(batch *batch.QuadBatch, _ []color2.Color, alpha float64) {
	scale := overlay.ruleset.GetBeatMap().Diff.CircleRadius / 64
	batch.SetScale(scale, scale)
	batch.SetColor(1, 1, 1, alpha)

	overlay.results.DrawTop(batch, 1.0)

	batch.Flush()

	if overlay.flashlight != nil {
		overlay.flashlight.Draw(batch.Projection)
	}

	prev := batch.Projection
	batch.SetCamera(overlay.camera.GetProjectionView())

	overlay.hitErrorMeter.Draw(batch, alpha)
	overlay.aimErrorMeter.Draw(batch, alpha)

	batch.SetScale(1, 1)
	batch.SetColor(1, 1, 1, alpha)

	if overlay.skip != nil {
		overlay.skip.Draw(overlay.lastTime, batch)
	}

	batch.SetCamera(prev)
}

func (overlay *ScoreOverlay) DrawHUD(batch *batch.QuadBatch, _ []color2.Color, alpha float64) {
	prev := batch.Projection
	batch.SetCamera(overlay.camera.GetProjectionView())
	batch.ResetTransform()
	batch.SetColor(1, 1, 1, alpha)

	overlay.entry.Draw(batch, alpha)

	overlay.passContainer.Draw(overlay.audioTime, batch)

	overlay.drawScore(batch, alpha)
	overlay.drawCombo(batch, alpha)
	overlay.hpBar.Draw(batch, alpha)
	overlay.drawKeys(batch, alpha)

	batch.ResetTransform()
	batch.SetColor(1, 1, 1, alpha)

	if settings.Gameplay.Mods.Show {
		batch.SetTranslation(vector.NewVec2d(settings.Gameplay.Mods.XOffset, settings.Gameplay.Mods.YOffset))
		overlay.mods.Draw(overlay.lastTime, batch)
		batch.ResetTransform()
	}

	if settings.Gameplay.ShowWarningArrows {
		overlay.arrows.Draw(overlay.audioTime, batch)
	}

	overlay.ppDisplay.Draw(batch, alpha)
	overlay.hitCounts.Draw(batch, alpha)

	if overlay.panel != nil {
		settings.Playfield.Bloom.Enabled = false
		overlay.panel.Draw(batch, overlay.resultsFade.GetValue())
	}

	batch.SetCamera(prev)
}

func (overlay *ScoreOverlay) drawScore(batch *batch.QuadBatch, alpha float64) {
	scoreAlpha := settings.Gameplay.Score.Opacity * alpha

	if scoreAlpha < 0.001 || !settings.Gameplay.Score.Show {
		return
	}

	xOff := settings.Gameplay.Score.XOffset
	yOff := settings.Gameplay.Score.YOffset

	scoreScale := settings.Gameplay.Score.Scale
	rightOffset := -9.6 * scoreScale

	progress := overlay.getProgress()

	scoreSize := overlay.scoreFont.GetSize() * scoreScale * 0.96
	scoreOverlap := overlay.scoreFont.Overlap * scoreSize / overlay.scoreFont.GetSize()

	accSize := scoreSize * 0.6
	accOverlap := overlay.scoreFont.Overlap * accSize / overlay.scoreFont.GetSize()
	accYPos := scoreSize + vAccOffset*scoreScale

	accOffset := overlay.ScaledWidth - overlay.scoreFont.GetWidthMonospaced(accSize, "99.99%") + accOverlap - 38.4*scoreScale + rightOffset

	batch.Flush()

	overlay.shapeRenderer.SetCamera(overlay.camera.GetProjectionView())

	if settings.Gameplay.Score.ProgressBar == "Pie" {
		if progress < 0.0 {
			overlay.shapeRenderer.SetColor(0.4, 0.8, 0.4, 0.6*scoreAlpha)
		} else {
			overlay.shapeRenderer.SetColor(1, 1, 1, 0.6*scoreAlpha)
		}

		overlay.shapeRenderer.Begin()
		overlay.shapeRenderer.DrawCircleProgressS(vector.NewVec2f(float32(accOffset+xOff), float32(accYPos+accSize/2+yOff)), 16*float32(settings.Gameplay.Score.Scale), 40, float32(progress))
		overlay.shapeRenderer.End()

		batch.SetColor(1, 1, 1, scoreAlpha)
		batch.SetScale(scoreScale, scoreScale)
		batch.SetTranslation(vector.NewVec2d(accOffset+xOff, accYPos+accSize/2+yOff))
		batch.DrawTexture(*overlay.circularMetre)

		accOffset -= 44.8 * scoreScale
	} else if progress > 0.0 {
		thickness := barThickness * scoreScale

		var positionX, positionY, bWidth float64

		switch settings.Gameplay.Score.ProgressBar {
		case "BottomRight":
			bWidth = barWidth * 0.694 * scoreScale
			positionX = overlay.ScaledWidth - bWidth
			positionY = 736
			bWidth = 188
		case "Bottom":
			positionX = 0
			positionY = overlay.ScaledHeight - thickness
			bWidth = overlay.ScaledWidth
		default:
			positionX = overlay.ScaledWidth - (12+barWidth)*scoreScale + xOff
			positionY = scoreSize - 2*scoreScale + yOff
			bWidth = barWidth * scoreScale
		}

		positionY += thickness / 2

		overlay.shapeRenderer.SetColor(1, 1, 0.5, 0.5*scoreAlpha)

		overlay.shapeRenderer.Begin()
		overlay.shapeRenderer.SetAdditive(true)
		overlay.shapeRenderer.DrawLine(float32(positionX), float32(positionY), float32(positionX+progress*bWidth), float32(positionY), float32(thickness))
		overlay.shapeRenderer.SetAdditive(false)
		overlay.shapeRenderer.End()
	}

	batch.ResetTransform()
	batch.SetColor(1, 1, 1, scoreAlpha)

	scoreText := fmt.Sprintf("%08d", int64(math.Round(overlay.scoreGlider.GetValue())))
	overlay.scoreFont.DrawOrigin(batch, overlay.ScaledWidth+rightOffset+scoreOverlap+xOff, yOff, vector.TopRight, scoreSize, true, scoreText)

	accText := fmt.Sprintf("%5.2f%%", overlay.accuracyGlider.GetValue())
	overlay.scoreFont.DrawOrigin(batch, overlay.ScaledWidth+rightOffset+accOverlap+xOff, accYPos+yOff, vector.TopRight, accSize, true, accText)

	batch.ResetTransform()
	batch.SetTranslation(vector.NewVec2d(accOffset+xOff, accYPos+accSize/2+yOff))
	batch.SetScale(scoreScale*0.8, scoreScale*0.8)

	if !settings.Gameplay.Score.ShowGradeAlways {
		overlay.rankBack.Draw(overlay.audioTime, batch)
		overlay.rankFront.Draw(overlay.audioTime, batch)
	} else if overlay.rankBack.Texture != nil {
		batch.DrawTexture(*overlay.rankBack.Texture)
	}
}

func (overlay *ScoreOverlay) drawCombo(batch *batch.QuadBatch, alpha float64) {
	comboAlpha := settings.Gameplay.ComboCounter.Opacity * alpha

	if comboAlpha < 0.001 || !settings.Gameplay.ComboCounter.Show {
		return
	}

	cmbSize := overlay.comboFont.GetSize() * settings.Gameplay.ComboCounter.Scale

	slideAmount := overlay.comboSlide.GetValue()

	if settings.Gameplay.ComboCounter.XOffset > 0.01 {
		slideAmount = 0
		comboAlpha *= 1 + overlay.comboSlide.GetValue()
	}

	posX := slideAmount*overlay.comboFont.GetWidth(cmbSize*overlay.newComboScale.GetValue(), fmt.Sprintf("%dx", overlay.combo)) + 2.5
	posY := overlay.ScaledHeight - 12.8
	origY := overlay.comboFont.GetSize()*0.375 - 9

	batch.ResetTransform()
	batch.SetTranslation(vector.NewVec2d(settings.Gameplay.ComboCounter.XOffset, settings.Gameplay.ComboCounter.YOffset))

	batch.SetAdditive(true)

	batch.SetColor(1, 1, 1, overlay.newComboFadeB.GetValue()*comboAlpha)
	overlay.comboFont.DrawOrigin(batch, posX-2.4*overlay.newComboScaleB.GetValue()*settings.Gameplay.ComboCounter.Scale, posY+origY*overlay.newComboScaleB.GetValue()*settings.Gameplay.ComboCounter.Scale, vector.BottomLeft, cmbSize*overlay.newComboScaleB.GetValue(), false, fmt.Sprintf("%dx", overlay.newCombo))

	batch.SetAdditive(false)

	batch.SetColor(1, 1, 1, comboAlpha)
	overlay.comboFont.DrawOrigin(batch, posX, posY+origY*overlay.newComboScale.GetValue()*settings.Gameplay.ComboCounter.Scale, vector.BottomLeft, cmbSize*overlay.newComboScale.GetValue(), false, fmt.Sprintf("%dx", overlay.combo))

	batch.ResetTransform()
}

func (overlay *ScoreOverlay) drawKeys(batch *batch.QuadBatch, alpha float64) {
	keyAlpha := settings.Gameplay.KeyOverlay.Opacity * alpha

	if keyAlpha < 0.001 || !settings.Gameplay.KeyOverlay.Show {
		return
	}

	batch.ResetTransform()

	batch.SetTranslation(vector.NewVec2d(settings.Gameplay.KeyOverlay.XOffset, settings.Gameplay.KeyOverlay.YOffset))

	keyScale := settings.Gameplay.KeyOverlay.Scale

	batch.SetColor(1, 1, 1, keyAlpha)
	batch.SetScale(keyScale, keyScale)

	overlay.keyOverlay.Draw(overlay.lastTime, batch)

	col := skin.GetInfo().InputOverlayText
	batch.SetColor(float64(col.R), float64(col.G), float64(col.B), keyAlpha)

	for i := 0; i < 4; i++ {
		posX := overlay.ScaledWidth - 24*keyScale
		posY := overlay.ScaledHeight/2 - 64 + (30.4+float64(i)*47.2)*keyScale
		scale := overlay.keys[i].GetScale().Y * keyScale

		text := strconv.Itoa(overlay.keyCounters[i])

		if overlay.keyCounters[i] == 0 || overlay.scoreEFont == nil {
			if overlay.keyCounters[i] == 0 {
				if i > 1 {
					text = "M"
				} else {
					text = "K"
				}

				text += strconv.Itoa(i%2 + 1)
			}

			overlay.keyFont.DrawOrigin(batch, posX, posY, vector.Centre, scale*14, true, text)
		} else {
			overlay.scoreEFont.Overlap = 1.6
			overlay.scoreEFont.DrawOrigin(batch, posX, posY, vector.Centre, scale*overlay.scoreEFont.GetSize(), false, text)
		}
	}

	batch.ResetTransform()
}

func (overlay *ScoreOverlay) getProgress() float64 {
	hObjects := overlay.ruleset.GetBeatMap().HitObjects
	startTime := hObjects[0].GetStartTime()
	endTime := hObjects[len(hObjects)-1].GetEndTime()

	musicPos := overlay.audioTime

	progress := mutils.ClampF64((musicPos-startTime)/(endTime-startTime), 0.0, 1.0)
	if musicPos < startTime {
		progress = mutils.ClampF64(-1.0+musicPos/startTime, -1.0, 0.0)
	}

	return progress
}

func (overlay *ScoreOverlay) isDrain() bool {
	hObjects := overlay.ruleset.GetBeatMap().HitObjects
	startTime := hObjects[0].GetStartTime() - overlay.ruleset.GetBeatMap().Diff.Preempt
	endTime := hObjects[len(hObjects)-1].GetEndTime() + float64(overlay.ruleset.GetBeatMap().Diff.Hit50)

	return overlay.audioTime >= startTime && overlay.audioTime <= endTime && !overlay.breakMode
}

func (overlay *ScoreOverlay) IsBroken(_ *graphics.Cursor) bool {
	return false
}

func (overlay *ScoreOverlay) showPassInfo() {
	if overlay.currentBreak.Length() < 2880 {
		return
	}

	pass := overlay.ruleset.GetHP(overlay.cursor) >= 0.5

	time := math.Min(overlay.currentBreak.GetEndTime()-2880, overlay.currentBreak.GetEndTime()-overlay.currentBreak.Length()/2)

	if pass {
		if !overlay.audioDisabled {
			overlay.passContainer.Add(sprite.NewAudioSprite(audio.LoadSample("sectionpass"), time+20, 1))
		}

		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+20, time+20, 0, 1))
		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+100, time+100, 1, 0))
		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+160, time+160, 0, 1))
		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+230, time+230, 1, 0))
		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+280, time+280, 0, 1))
		overlay.sPass.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+1280, time+1480, 1, 0))
	} else {
		if !overlay.audioDisabled {
			overlay.passContainer.Add(sprite.NewAudioSprite(audio.LoadSample("sectionfail"), time+130, 1))
		}

		overlay.sFail.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+130, time+130, 0, 1))
		overlay.sFail.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+230, time+230, 1, 0))
		overlay.sFail.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+280, time+280, 0, 1))
		overlay.sFail.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+1280, time+1480, 1, 0))
	}

	if overlay.currentBreak.Length() < 5000 {
		return
	}

	breakStart := overlay.currentBreak.GetStartTime()
	breakEnd := overlay.currentBreak.GetEndTime()

	overlay.rankBack.AddTransform(animation.NewSingleTransform(animation.Fade, easing.OutQuad, breakStart+400, breakStart+1600, 1, 0))
	overlay.rankBack.AddTransform(animation.NewSingleTransform(animation.Scale, easing.OutQuad, breakStart+400, breakStart+1600, 1, 1.625))

	overlay.rankFront.AddTransform(animation.NewSingleTransform(animation.Fade, easing.OutQuad, breakStart+400, breakStart+700, 0, 1))
	overlay.rankFront.AddTransform(animation.NewSingleTransform(animation.Fade, easing.InQuad, breakEnd-1000, breakEnd-700, 1, 0))
}

func (overlay *ScoreOverlay) initMods() {
	mods := overlay.ruleset.GetBeatMap().Diff.Mods.StringFull()

	scale := settings.Gameplay.Mods.Scale
	alpha := settings.Gameplay.Mods.Opacity

	offset := -48.0 * scale
	for i, s := range mods {
		modSpriteName := "selection-mod-" + strings.ToLower(s)

		mod := sprite.NewSpriteSingle(skin.GetTexture(modSpriteName), float64(i), vector.NewVec2d(overlay.ScaledWidth+offset, 150), vector.Centre)
		mod.SetAlpha(0)
		mod.ShowForever(true)

		timeStart := overlay.audioTime + float64(i)*500

		mod.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, timeStart, timeStart+400, 0.0, 1.0*alpha))
		mod.AddTransform(animation.NewSingleTransform(animation.Scale, easing.OutQuad, timeStart, timeStart+400, 2*scale, 1.0*scale))

		if (overlay.cursor.IsPlayer && !overlay.cursor.IsAutoplay) || settings.Gameplay.Mods.HideInReplays {
			startT := overlay.ruleset.GetBeatMap().HitObjects[0].GetStartTime()
			mod.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, startT, startT+5000, 1.0*alpha, 0))
		}

		if overlay.cursor.Name == "" || settings.Gameplay.Mods.FoldInReplays {
			offset -= 16 * scale
		} else {
			offset -= 80 * scale
		}

		overlay.mods.Add(mod)
	}
}

func (overlay *ScoreOverlay) initArrows() {
	overlay.arrows = sprite.NewManager()

	createArrow := func(tex *texture.TextureRegion, color color2.Color, position vector.Vector2d, flip bool) *sprite.Sprite {
		arrow := sprite.NewSpriteSingle(tex, 9999, position, vector.Centre)
		arrow.SetHFlip(flip)
		arrow.SetColor(color)
		arrow.SetAlpha(0)

		return arrow
	}

	arrowTexture := skin.GetTexture("arrow-warning")
	color := color2.NewL(1)

	if arrowTexture == nil {
		arrowTexture = skin.GetTexture("play-warningarrow")

		if skin.GetInfo().Version >= 2.0 {
			color = color2.NewRGB(1, 0, 0)
		}
	}

	arrows := []*sprite.Sprite{
		createArrow(arrowTexture, color, vector.NewVec2d(128, 160), false),
		createArrow(arrowTexture, color, vector.NewVec2d(128, overlay.ScaledHeight-160), false),
		createArrow(arrowTexture, color, vector.NewVec2d(overlay.ScaledWidth-128, 160), true),
		createArrow(arrowTexture, color, vector.NewVec2d(overlay.ScaledWidth-128, overlay.ScaledHeight-160), true),
	}

	for _, arrow := range arrows {
		overlay.arrows.Add(arrow)
	}

	addTransforms := func(start float64, times int) {
		for _, arrow := range arrows {
			for i := 0; i < times; i++ {
				time := start + float64(i)*blinkTime
				arrow.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time, time, 1, 1))
				arrow.AddTransform(animation.NewSingleTransform(animation.Fade, easing.Linear, time+blinkTime/2, time+blinkTime/2, 0, 0))
			}
		}
	}

	bMap := overlay.ruleset.GetBeatMap()

	if bMap.HitObjects[0].GetStartTime() > 6000 {
		addTransforms(bMap.HitObjects[0].GetStartTime()-bMap.Diff.Preempt-900, minBlinks+mutils.MinI(2, int(bMap.Diff.Preempt/blinkTime)))
	}

	for _, pause := range bMap.Pauses {
		blinks := mutils.MinI(minBlinks, int(pause.Length()/blinkTime))
		extra := mutils.MinI(2, int(bMap.Diff.Preempt/blinkTime))
		addTransforms(pause.EndTime-float64(blinks)*blinkTime, blinks+extra)
	}
}

func (overlay *ScoreOverlay) DisableAudioSubmission(b bool) {
	overlay.audioDisabled = b
}

func (overlay *ScoreOverlay) SetBeatmapEnd(end float64) {
	overlay.beatmapEnd = end
}

func (overlay *ScoreOverlay) ShouldDrawHUDBeforeCursor() bool {
	return true
}
