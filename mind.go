package main

// The decision layer. Each tick: read the lobby, price the threats, choose one behaviour
// (zone run, break contact, fight, farm, lie low), then hand off to legs and weapons.
//
// Doctrine in one paragraph: placement is counted in mechs that die before us, so against a
// hunting pack we trade blows whenever the exchange favours us and snowball on cubes; we break
// contact on a hit-point hysteresis (leave when the numbers turn, come back only when repaired)
// and spend that time behind hard cover. When the lobby turns out to be a true free-for-all we
// stop starting fights: defend, finish cripples, third-party the wounded, otherwise stay unseen.

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

var traceOut = func() *os.File {
	if path := os.Getenv("KTRACE"); path != "" {
		f, _ := os.Create(strings.Replace(path, "%p", strconv.Itoa(os.Getpid()), 1))
		return f
	}
	return nil
}()

type zoneRead struct {
	outNow, outNext float64 // metres outside the current / next circle (negative = inside)
	passIn          float64 // seconds until the closing edge reaches us
	critical        bool
	urgent          bool
	final           bool
}

func (p *Pilot) readZone() zoneRead {
	z := &p.f.Zone
	var zr zoneRead
	cx, cy, r := p.zoneNow(0)
	zr.outNow = gap2(p.x, p.y, cx, cy) - r
	zr.outNext = gap2(p.x, p.y, z.NextCenter[0], z.NextCenter[1]) - z.NextRadius
	zr.passIn = 999
	for a := 0.0; a <= 60; a += 0.5 {
		cx, cy, r = p.zoneNow(a)
		if gap2(p.x, p.y, cx, cy) > r-1 {
			zr.passIn = a
			break
		}
	}
	need := (zr.outNext+8)/p.walk*1.35 + 4
	zr.final = z.NextRadius < 12
	zr.critical = zr.outNow > -2 || zr.passIn < 2.5
	zr.urgent = zr.critical || zr.passIn < 11 ||
		(zr.outNext > 0 && z.ShrinkEnd-p.t < need) || (zr.outNext > 0 && zr.passIn-need < 3)
	return zr
}

// ringSpot picks where to wait out the zone: on the near side of the next circle, well inside
// its rim but far from the middle, so the walk is short and enemies are mostly on one side.
func (p *Pilot) ringSpot() [2]float64 {
	if p.t-p.zoneGoalT < 1 {
		return p.zoneGoal
	}
	z := &p.f.Zone
	R := z.NextRadius
	ring := math.Max(0, math.Min(R-math.Min(10, R/2)-12, 0.8*R))
	base := math.Atan2(p.y-z.NextCenter[1], p.x-z.NextCenter[0])
	here := p.L.Snap(p.x, p.y, p.z, 3)
	pick := [2]float64{z.NextCenter[0], z.NextCenter[1]}
	found := false
	for _, fr := range [6]float64{1, 0.8, 0.6, 0.4, 0.2, 0} {
		for _, off := range [9]float64{0, 0.35, -0.35, 0.8, -0.8, 1.4, -1.4, 2.2, -2.2} {
			x := z.NextCenter[0] + math.Cos(base+off)*ring*fr
			y := z.NextCenter[1] + math.Sin(base+off)*ring*fr
			k := p.L.Snap(x, y, math.NaN(), 2)
			if k < 0 {
				continue
			}
			if here >= 0 && p.L.Region[k] != p.L.Region[here] {
				continue
			}
			nx, ny := p.L.XY(k)
			if gap2(nx, ny, z.NextCenter[0], z.NextCenter[1]) > R-math.Min(5, 0.3*R) {
				continue
			}
			pick = [2]float64{nx, ny}
			found = true
			break
		}
		if found {
			break
		}
	}
	p.zoneGoal, p.zoneGoalT = pick, p.t
	return pick
}

// findLair floods outward for the nearest spot no known enemy can see.
func (p *Pilot) findLair(maxCost float64, mustGainRange bool) int {
	type eye struct{ x, y, z, d float64 }
	var eyes []eye
	for _, c := range p.contacts {
		if c.Drone || c.D > 260 || (!c.Ghost && p.t-c.ViewT > 6) {
			continue
		}
		eyes = append(eyes, eye{c.Pos[0], c.Pos[1], c.Pos[2] + 8*c.scale(&p.R), c.D})
	}
	if len(eyes) == 0 {
		return -1
	}
	for len(eyes) > 4 {
		far := 0
		for i := range eyes {
			if eyes[i].d > eyes[far].d {
				far = i
			}
		}
		eyes[far] = eyes[len(eyes)-1]
		eyes = eyes[:len(eyes)-1]
	}
	z := &p.f.Zone
	soon := z.ShrinkEnd-p.t < 25
	best, bestScore, found := -1, math.Inf(1), 0
	start := p.L.Snap(p.x, p.y, p.z, 3)
	p.L.Spill(start, float32(maxCost), int(knob("lairVisits", 900)), func(k int, cost float32) bool {
		if (k%p.L.W)%2 != 0 || (k/p.L.W)%2 != 0 || p.L.Tight[k] && cost > 4 {
			return false
		}
		x, y := p.L.XY(k)
		lvl := float64(p.L.Lvl[k])
		if !p.insideLater(x, y, float64(cost)/p.walk+2, 6) {
			return false
		}
		if soon && gap2(x, y, z.NextCenter[0], z.NextCenter[1]) > z.NextRadius-4 {
			return false
		}
		nearest := math.Inf(1)
		for _, e := range eyes {
			if p.A.Clear(e.x, e.y, e.z, x, y, lvl+0.5*p.bodyH) || p.A.Clear(e.x, e.y, e.z, x, y, lvl+0.95*p.bodyH) {
				return false
			}
			d := gap2(e.x, e.y, x, y)
			if mustGainRange && e.d <= 80 && d < e.d+18 {
				return false
			}
			nearest = math.Min(nearest, d)
		}
		score := float64(cost) - 0.15*math.Min(nearest, 150) +
			0.6*math.Max(0, gap2(x, y, z.NextCenter[0], z.NextCenter[1])-z.NextRadius+10)
		if k == p.lair {
			score -= 10
		}
		if score < bestScore {
			best, bestScore = k, score
		}
		found++
		return found >= int(knob("lairPick", 10))
	})
	return best
}

// hiddenAt: is a mech standing at the node invisible to every recently seen enemy?
func (p *Pilot) hiddenHere() bool {
	for _, c := range p.contacts {
		if c.Drone || c.Ghost || p.t-c.ViewT > 6 || c.D > 260 {
			continue
		}
		ez := c.Pos[2] + 8*c.scale(&p.R)
		if p.A.Clear(c.Pos[0], c.Pos[1], ez, p.x, p.y, p.z+0.5*p.bodyH) || p.A.Clear(c.Pos[0], c.Pos[1], ez, p.x, p.y, p.z+0.95*p.bodyH) {
			return false
		}
	}
	return true
}

func (p *Pilot) Decide(f *Frame) Order {
	p.absorb(f)
	me := p.me
	o := Order{Pick: -1, Take: -1, Drop: -1, Yaw: me.Aim[0], Pitch: 0}
	p.pickUpgrade(&o)
	p.grabArms(&o)
	zr := p.readZone()

	// ---------------------------------------------------------------- lobby stance
	duel := f.Alive <= 2
	shy := p.melee && !duel && p.t < 360 // free-for-all manners
	wantCubes := int(knob("cubes", 26))
	if shy {
		wantCubes = 12
	}

	// ---------------------------------------------------------------- threat pricing
	var hot []*Contact
	var mark *Contact
	markScore := 0.0
	ghostAbout := false
	hotIntake := 0.0
	othersNear := 0
	for _, c := range p.contacts {
		if c.Drone {
			continue
		}
		c.Adv = p.edge(c)
		toward := math.Atan2(p.y-c.Pos[1], p.x-c.Pos[0])
		closing := -((c.Pos[0]-p.x)*(c.Vel[0]-me.Vel[0]) + (c.Pos[1]-p.y)*(c.Vel[1]-me.Vel[1])) / math.Max(c.D, 1)
		lit := c.Sight && c.Listed
		reach := 200.0
		if c.Laser {
			reach = 285
		}
		is := p.t-c.StruckT < 4 ||
			(lit && p.t-c.AimT < 0.6 && c.D < reach) ||
			(lit && c.D < 40) ||
			(lit && c.D < 90 && math.Abs(turn(c.Yaw-toward)) < 0.5 && closing > 4)
		if c.Ghost {
			is = true
			c.Adv = math.Min(c.Adv, 0.5)
			ghostAbout = true
		}
		if is {
			c.hotT = p.t
		}
		c.Hot = p.t-c.hotT < 0.8
		if c.Hot {
			hot = append(hot, c)
			hotIntake += p.intakeFrom(c, c.D)
		}
		if p.t-c.ViewT < 4 && c.D < 160 {
			othersNear++
		}
	}
	// who may we shoot at?
	allowed := func(c *Contact) bool {
		if !shy {
			return true
		}
		frac := c.HP / math.Max(c.Max, 1)
		switch {
		case p.t-c.StruckT < 5:
			return c.D < 45 || p.hpf > 0.5
		case c.D < 40 && c.Hot:
			return true
		case frac < 0.25 && p.hpf > 0.85 && othersNear <= 1 && c.D < 150:
			return true // a sure kill with nobody watching
		case p.t-c.OtherT < 3 && frac < 0.5 && p.hpf > 0.75 && c.D < 140:
			return true // someone else already did the work
		case c.Adv >= 3 && c.D < 120:
			return true
		}
		return false
	}
	for _, c := range p.contacts {
		if c.Drone || c.Ghost || !c.Listed || c.D > 285 || !allowed(c) {
			continue
		}
		s := 1 / (1 + c.D/knob("tgtD", 60)) * (1 + knob("weakW", 0.6)*(1-c.HP/math.Max(c.Max, 1)))
		if c.Sight {
			s *= knob("sightW", 1.5)
		}
		if p.t-c.StruckT < 5 {
			s *= knob("struckW", 1.3)
		}
		if c.ID == p.focus {
			s *= knob("stick", 1.25)
		}
		if c.Mortar && p.alt != nil && p.alt.Kind == "laser" {
			s *= 1.15
		}
		if s > markScore {
			mark, markScore = c, s
		}
	}
	if mark != nil {
		if mark.ID != p.focus {
			p.focus, p.focusT = mark.ID, p.t
		}
	} else {
		p.focus = -1
	}

	underFire := p.t-p.hurtT < 3
	danger := len(hot) > 0 || underFire
	overall := 10.0
	if hotIntake > 1 {
		theirs := 0.0
		for _, c := range hot {
			theirs += math.Max(c.HP, 20) / math.Max(p.outputAt(c.D, false), 1)
		}
		overall = (me.HP / hotIntake) / math.Max(theirs, 0.01)
		if len(hot) == 1 && hot[0] == mark {
			overall = mark.Adv
		}
	}
	lastLegs := math.Max(p.pressure/3, 1)
	ttd := me.HP / lastLegs
	myOut := 0.0
	if mark != nil {
		myOut = p.outputAt(mark.D, false)
	}
	finishable := mark != nil && mark.Sight && mark.HP < myOut*1.2+40 && p.hpf > 0.12

	// ---------------------------------------------------------------- break contact?
	enterHP, exitHP := knob("enter", 0.42), knob("exit", 0.72)
	if shy {
		enterHP, exitHP = 0.75, 0.93
	}
	if duel {
		enterHP, exitHP = 0.0, 0.5
	}
	if !p.fleeing {
		if danger && p.t-p.cornerT > 4 {
			leave := false
			switch {
			case ghostAbout && underFire && (ttd < 15 || p.hpf < 0.6):
				leave = true
			case len(hot) >= 2:
				leave = (ttd < knob("multiTTD", 10) && p.hpf < knob("multiHP", 0.8)) || (p.hpf < 0.2 && ttd < 20) || (shy && p.hpf < 0.65)
			case len(hot) == 1:
				leave = overall < knob("duelOut", 0.5) && hot[0].D > 30 && ttd < 25
			}
			// the hit-point floor: below it we only stay for a fight we are clearly winning
			if p.hpf < enterHP && overall < knob("stayAdv", 1.6) {
				leave = true
			}
			if leave && !finishable && !duel {
				weakFoe := len(hot) <= 1 && mark != nil && mark.Sight && mark.HP < me.HP*0.6
				if !weakFoe {
					p.fleeing, p.fleeT0 = true, p.t
					p.lair = -1
				}
			}
		}
	} else {
		winnable := overall > 0.95
		stop := false
		switch {
		case p.hpf >= exitHP && (!danger || winnable):
			stop = true
		case !shy && winnable && p.hpf > 0.6 && len(hot) <= 1 && !ghostAbout:
			stop = true
		case finishable:
			stop = true
		case duel:
			stop = true
		}
		if !stop && mark != nil && mark.Sight && mark.D < 45 && p.t-p.fleeT0 > 2 && len(hot) <= 1 && overall > 0.35 {
			stop = true // cornered: turn and fight
			p.cornerT = p.t
		}
		if stop {
			p.fleeing = false
		}
	}

	// ---------------------------------------------------------------- fight or not
	wantFight := false
	if mark != nil && !p.fleeing {
		switch {
		case duel:
			wantFight = mark.Hot || p.hpf > 0.5
		case mark.Hot:
			wantFight = true
			if shy && p.t-mark.StruckT > 4 && mark.D > 85 && mark.Adv < 3 {
				wantFight = false // it is looking, not shooting: do not start it
			}
		default:
			ratio := knob("hunt", 0.7)
			if shy {
				ratio = 1.3
			}
			wantFight = mark.Adv >= ratio && mark.D < knob("huntD", 130) && (mark.Sight || knob("huntBlind", 0) > 0) && p.hpf > knob("huntHP", 0.6)
			if knob("huntArmed", 0) > 0 && p.alt == nil && p.t < knob("huntArmedT", 90) {
				wantFight = false // do not pick fights with the starter gun alone
			}
		}
	}
	// free-for-all bubble: anyone in view within 220 m means get out of sight
	avoid := false
	if shy && !wantFight && !p.fleeing {
		for _, c := range p.contacts {
			if !c.Drone && !c.Ghost && p.t-c.ViewT < 1 && c.D < 220 {
				avoid = true
			}
		}
	}

	// ---------------------------------------------------------------- choose movement
	dv := Drive{Kind: 'h', Caution: 1, Pet: -1, Weight: 1.6}
	permit := false     // may open fire on the mark
	var prize *Prize    // loot being worked
	var crateMark *Mark // non-mech thing to shoot
	note := "idle"
	sprintOK := func(min float64) bool { return me.Energy > min }

	goTo := func(x, y, w float64, sprint bool) {
		dv.Kind, dv.GX, dv.GY, dv.Weight, dv.Sprint = 'g', x, y, w, sprint
	}
	hideOut := func(maxCost float64, tag string) {
		if p.lair < 0 || p.t-p.lairT > 0.6 {
			p.lair = p.findLair(maxCost, p.fleeing)
			p.lairT = p.t
		}
		dv.Caution = knob("cautFlee", 2.5)
		if p.lair >= 0 {
			lx, ly := p.L.XY(p.lair)
			if gap2(lx, ly, p.x, p.y) < 1.2 || (gap2(lx, ly, p.x, p.y) < 3 && p.hiddenHere()) {
				dv.Kind, dv.Still = 'h', true
				note = tag + ":hold"
			} else {
				goTo(lx, ly, 2.2, underFire && sprintOK(20))
				note = tag + ":cover"
			}
			return
		}
		// nothing hidden in reach: open the range, bending toward the safe side of the zone
		var foe *Contact
		for _, c := range p.contacts {
			if !c.Drone && (c.Ghost || p.t-c.ViewT < 6) && (foe == nil || c.D < foe.D) {
				foe = c
			}
		}
		if foe == nil {
			if p.hiddenHere() && zr.outNext < -5 {
				dv.Kind, dv.Still = 'h', true
				note = tag + ":rest"
			} else {
				g := p.ringSpot()
				goTo(g[0], g[1], 1.6, false)
				note = tag + ":ring"
			}
			return
		}
		ax, ay := p.x-foe.Pos[0], p.y-foe.Pos[1]
		if knob("fleeGrad", 0) > 0 {
			// minimum-risk heading: every known shooter pushes us away, weighted by its
			// firepower and the inverse square of its distance
			ax, ay = 0, 0
			for _, c := range p.contacts {
				if c.Drone || !(c.Ghost || p.t-c.ViewT < 6) {
					continue
				}
				d := math.Max(c.D, 10)
				w := math.Max(p.intakeFrom(c, c.D), 20) / (d * d)
				ax += (p.x - c.Pos[0]) / d * w
				ay += (p.y - c.Pos[1]) / d * w
			}
		}
		if n := math.Hypot(ax, ay); n > 1e-6 {
			ax, ay = ax/n, ay/n
		}
		g := p.ringSpot()
		sx, sy := g[0]-p.x, g[1]-p.y
		if n := math.Hypot(sx, sy); n > 1e-6 {
			sx, sy = sx/n, sy/n
		}
		bx, by := ax+0.4*sx, ay+0.4*sy
		if n := math.Hypot(bx, by); n > 1e-6 {
			bx, by = bx/n, by/n
		}
		goTo(p.x+bx*75, p.y+by*75, 2.0, underFire && sprintOK(30))
		note = tag + ":run"
	}

	var armGoal *Stash
	if far := knob("armFirst", 200); far > 0 && !finishable && !(mark != nil && mark.Sight && mark.D < knob("armYield", 70)) {
		armGoal = p.armRun(far, knob("armAny", 60))
	}
	switch {
	case zr.critical || (zr.urgent && (!wantFight || mark.D > 60)) || (zr.urgent && zr.passIn < 5):
		g := p.ringSpot()
		goTo(g[0], g[1], 3.0, zr.critical && sprintOK(15))
		dv.Caution = 0.4
		permit = mark != nil && (mark.Hot || wantFight)
		p.banned[p.lootID] = p.t + 20
		p.lootID = -1
		note = "zone"

	case p.fleeing:
		hideOut(knob("lairCost", 90), "flee")
		// quiet retreat: answer fire, do not advertise
		if mark != nil {
			permit = p.t-mark.StruckT < 3 || mark.D < 60 || (mark.Sight && p.t-mark.AimT < 0.5 && mark.HP < 250) || knob("fleeFire", 0) > 0
		}
		if note == "flee:run" && mark != nil && knob("runFight", 0) > 0 {
			// nowhere to hide: a fighting withdrawal beats being shot in the back
			permit = true
			if knob("runFight", 0) > 1 {
				dv = Drive{Kind: 's', Around: mark, RMin: knob("runLo", 110), RMax: knob("runHi", 160), Weight: 1.8, Caution: 1.5, Pet: -1,
					Sprint: mark.D < 60 && sprintOK(45)}
				note = "flee:trade"
			}
		}

	case armGoal != nil:
		// arm-first opening: a laser decides most fights, so fetch one before trading
		d := gap2(armGoal.Pos[0], armGoal.Pos[1], p.x, p.y)
		goTo(armGoal.Pos[0], armGoal.Pos[1], 2.2, d > 25 && sprintOK(30))
		permit = mark != nil && (mark.Hot || wantFight)
		p.lootID = armGoal.ID
		note = "arm"

	case wantFight:
		permit = true
		dv.Seek = mark
		dv.Caution = knob("cautFight", 1)
		lo, hi := knob("lo", 45), knob("hi", 80)
		if p.alt != nil && p.alt.Kind == "lightning" && me.Energy > 30 {
			lo, hi = 14, 34
		} else if mark.Bolt {
			lo, hi = 56, 78 // stay outside the arc
		}
		multi := len(hot) >= int(knob("multi", 2))
		var arm *Stash
		if p.alt == nil {
			for _, s := range p.stash {
				if s.Class == 'w' && p.wantsArm(s.Kind) && gap2(s.Pos[0], s.Pos[1], p.x, p.y) < 32 &&
					math.Abs(s.Pos[2]-p.z) < 4 && (arm == nil || armWorth(s.Kind) > armWorth(arm.Kind)) {
					arm = s
				}
			}
		}
		cube := p.nearCubes(wantCubes)
		switch {
		case arm != nil:
			goTo(arm.Pos[0], arm.Pos[1], 2.0, false)
			p.lootID = arm.ID
			note = "fight+arm"
		case cube != nil && !(multi && p.hpf < 0.5):
			goTo(cube.Pos[0], cube.Pos[1], 2.0, false)
			p.lootID = cube.ID
			note = "fight+cube"
		case multi:
			// outnumbered: stretch the range around the closest of them and keep trading
			nearest := hot[0]
			for _, c := range hot {
				if c.D < nearest.D {
					nearest = c
				}
			}
			dv.Kind, dv.Around = 's', nearest
			dv.RMin, dv.RMax = knob("kiteLo", 70), knob("kiteHi", 115)
			if p.alt != nil && p.alt.Kind == "laser" {
				dv.RMin, dv.RMax = knob("kiteLLo", 125), knob("kiteLHi", 185)
			}
			if p.alt != nil && p.alt.Kind == "lightning" && me.Energy > 40 {
				dv.RMin, dv.RMax = lo, hi
			}
			dv.Weight = 1.8
			dv.Seek = nil
			dv.Sprint = nearest.D < 60 && sprintOK(45)
			dv.Caution = 1.4
			note = "kite"
		default:
			dv.Kind, dv.Around, dv.RMin, dv.RMax, dv.Weight = 's', mark, lo, hi, 1.6
			dv.Sprint = p.alt != nil && p.alt.Kind == "lightning" && me.Energy > 55 && mark.D > hi+10
			note = "fight"
		}

	case p.t-p.droneHurtT < 4 && p.nearestDrone() != nil && !avoid:
		dr := p.nearestDrone()
		dv.Kind, dv.Around, dv.RMin, dv.RMax, dv.Weight = 's', dr, 35, 60, 1.4
		dv.Pet = dr.ID
		dv.Seek = dr
		m := p.markOf(dr)
		crateMark = &m
		note = "pest"

	case avoid:
		hideOut(70, "shy")
		if mark != nil {
			permit = p.t-mark.StruckT < 3 || mark.D < 45
		}

	default:
		maxD := 250.0
		if shy {
			maxD = 200
		}
		prize = p.choosePrize(wantCubes, danger, shy, maxD)
		if prize != nil {
			if prize.ID != p.lootID {
				p.lootID, p.lootT = prize.ID, p.t
			}
			// give up on things that take forever
			if p.t-p.lootT > 45 {
				p.banned[prize.ID] = p.t + 40
				prize = nil
			}
		}
		switch {
		case prize != nil:
			d := gap2(prize.Pos[0], prize.Pos[1], p.x, p.y)
			switch prize.Class {
			case 'k':
				s := prize.Item
				lit := p.A.Clear(p.x, p.y, p.z+p.muzzleH, s.Pos[0], s.Pos[1], s.Pos[2]+1.2)
				if d < 70 && lit {
					crateMark = &Mark{Pos: s.Pos, Radius: 1.4, MidZ: 1.2, HP: s.HP, Sight: true, Recent: true, Lead: -1}
				}
				if d > 9 || !lit {
					goTo(s.Pos[0], s.Pos[1], 1.6, false)
				}
				note = "crate"
			case 'd':
				c := prize.Drone
				m := p.markOf(c)
				if c.D < 110 {
					crateMark = &m
				}
				dv.Kind, dv.Around, dv.RMin, dv.RMax, dv.Weight = 's', c, 48, 70, 1.4
				dv.Pet, dv.Seek = c.ID, c
				note = "drone"
			case 's':
				if d > 3 {
					goTo(prize.Pos[0], prize.Pos[1], 2.0, false)
				} else {
					dv.Still = true
				}
				note = "drop"
			default:
				goTo(prize.Pos[0], prize.Pos[1], 1.8, prize.Kind == "laser" && d > 25 && sprintOK(30))
				note = "loot"
			}
		case zr.final:
			z := &f.Zone
			if gap2(p.x, p.y, z.NextCenter[0], z.NextCenter[1]) > 3.5 {
				goTo(z.NextCenter[0], z.NextCenter[1], 2.4, false)
			} else {
				dv.Still = true
			}
			note = "final"
		default:
			roam := p.hpf >= 0.7 && me.Cubes < wantCubes && !shy && knob("roam", 1) > 0
			if roam {
				if g, ok := p.roamGoal(); ok {
					goTo(g[0], g[1], 1.4, false)
					note = "roam"
					break
				}
			}
			g := p.ringSpot()
			if gap2(g[0], g[1], p.x, p.y) > 6 {
				goTo(g[0], g[1], 1.4, false)
				// tread softly when someone is close enough to hear footsteps
				if p.t-p.soundT < 4 && p.t-p.anyHurtT > 4 && zr.outNext < 0 {
					dv.Crawl = clamp(4.6/p.walk, 0.2, 1)
				}
				note = "ring"
			} else {
				dv.Still = true
				note = "wait"
			}
		}
		if mark != nil && mark.Hot {
			permit = true
		}
	}

	p.steer(&o, &dv)

	// ---------------------------------------------------------------- torso and triggers
	blindside := underFire && p.hurtBy >= 0 && p.t-p.hurtT < 2.5
	if blindside {
		if c := p.contacts[p.hurtBy]; c != nil && c.Listed && c.Sight {
			blindside = false
		}
	}
	switch {
	case blindside && (mark == nil || !mark.Sight || p.hurtBy != mark.ID && !wantFight):
		// hit from somewhere we are not looking: turn the front armour to it
		o.Yaw, o.Pitch = p.hurtYaw, 0
		p.holdCharge(&o)
	case crateMark != nil && (mark == nil || !mark.Hot):
		p.shoot(&o, crateMark, true)
	case mark != nil && (wantFight || mark.Hot || p.fleeing || permit):
		m := p.markOf(mark)
		p.shoot(&o, &m, permit)
	case mark != nil:
		// keep the front toward the most dangerous thing in view without shooting
		m := p.markOf(mark)
		p.shoot(&o, &m, false)
	default:
		p.gaze(&o)
		p.holdCharge(&o)
		if g := p.gun; g != nil && g.Reload <= 0 && g.Ammo < 0.67*g.Stats.Mag {
			o.Reload = true
		}
	}
	// top the magazine up whenever nobody has had a line on us for a moment
	if g := p.gun; g != nil && g.Reload <= 0 && g.Ammo <= 0.5*g.Stats.Mag && !o.Fire0 && !o.Fire1 {
		quiet := true
		for _, c := range p.contacts {
			if !c.Drone && p.t-c.SightT < 1.1 {
				quiet = false
			}
		}
		if quiet {
			o.Reload = true
		}
	}
	o.Note = note
	if traceOut != nil && int(f.Tick)%10 == 0 {
		md, ma, mh := -1.0, 0.0, 0.0
		if mark != nil {
			md, ma, mh = mark.D, mark.Adv, mark.HP
		}
		w := "-"
		if p.alt != nil {
			w = p.alt.Kind
		}
		fmt.Fprintf(traceOut, "t=%5.1f hp=%4.0f en=%3.0f cub=%d %-10s hot=%d ov=%.2f mark=%.0fm adv=%.2f hp=%.0f alive=%d alt=%s melee=%v zone(out=%.0f pass=%.0f) fire=%v%v\n",
			p.t, me.HP, me.Energy, me.Cubes, note, len(hot), overall, md, ma, mh, f.Alive, w, p.melee, zr.outNow, zr.passIn, o.Fire0, o.Fire1)
	}
	return o
}

func (p *Pilot) nearestDrone() *Contact {
	var best *Contact
	for _, c := range p.contacts {
		if c.Drone && c.Listed && c.D < 120 && (best == nil || c.D < best.D) {
			best = c
		}
	}
	return best
}

// roamGoal sends us to the map cell (inside the coming circle) we have not looked at for longest.
func (p *Pilot) roamGoal() ([2]float64, bool) {
	if p.t-p.roamT < 6 && gap2(p.roamTo[0], p.roamTo[1], p.x, p.y) > 20 && p.insideLater(p.roamTo[0], p.roamTo[1], 20, 10) {
		return p.roamTo, true
	}
	z := &p.f.Zone
	here := p.L.Snap(p.x, p.y, p.z, 3)
	best, bestScore := [2]float64{}, 0.0
	for a := 0; a < 12; a++ {
		for _, fr := range [3]float64{0.3, 0.6, 0.85} {
			ang := float64(a) * math.Pi / 6
			x := z.NextCenter[0] + math.Cos(ang)*z.NextRadius*fr
			y := z.NextCenter[1] + math.Sin(ang)*z.NextRadius*fr
			k := p.L.Snap(x, y, math.NaN(), 2)
			if k < 0 || here < 0 || p.L.Region[k] != p.L.Region[here] {
				continue
			}
			seen, ok := p.seenCell[p.cell64(x, y)]
			age := 200.0
			if ok {
				age = p.t - seen
			}
			if age < 45 {
				continue
			}
			d := gap2(x, y, p.x, p.y)
			if d < 30 {
				continue
			}
			if s := age / (d + 60); s > bestScore {
				best, bestScore = [2]float64{x, y}, s
			}
		}
	}
	if bestScore <= 0 {
		return best, false
	}
	p.roamTo, p.roamT = best, p.t
	return best, true
}

// gaze decides where to look when there is nothing to shoot.
func (p *Pilot) gaze(o *Order) {
	me := p.me
	o.Pitch = 0
	switch {
	case p.t < 1.8:
		o.Yaw = turn(me.Aim[0] + 0.39) // opening spin: see who spawned nearby
	case p.t-p.hurtT < 2.5:
		o.Yaw = p.hurtYaw
	case p.t-p.soundT < 1.5:
		o.Yaw = p.soundYaw
	default:
		// every few seconds do a full turn, otherwise sweep ahead of our travel
		every := 7.0
		if p.alt != nil && p.alt.Kind == "laser" {
			every = 13
		}
		if p.t-p.sweepT > every {
			p.sweepT = p.t
		}
		if p.t-p.sweepT < 1.7 {
			o.Yaw = turn(me.Aim[0] + 0.39)
			return
		}
		base := p.scanBase
		if math.Hypot(p.lastMove[0], p.lastMove[1]) > 0.3 {
			base = math.Atan2(p.lastMove[1], p.lastMove[0])
			p.scanBase = base
		}
		o.Yaw = turn(base + 1.7*math.Sin(1.2*p.t))
	}
}

// armRun finds a weapon worth dropping everything for: a laser within far metres when we have
// none, or any wanted secondary within near metres when the slot is empty.
func (p *Pilot) armRun(far, near float64) *Stash {
	if p.alt != nil && p.alt.Kind == "laser" {
		return nil
	}
	here := p.L.Snap(p.x, p.y, p.z, 3)
	var best *Stash
	bestScore := 0.0
	for _, s := range p.stash {
		if s.Class != 'w' || !p.wantsArm(s.Kind) {
			continue
		}
		if until, bad := p.banned[s.ID]; bad && p.t < until {
			continue
		}
		d := gap2(s.Pos[0], s.Pos[1], p.x, p.y)
		lim := near
		if s.Kind == "laser" {
			lim = far
		} else if p.alt != nil {
			continue
		}
		if d > lim || !p.insideLater(s.Pos[0], s.Pos[1], 1.25*d/p.walk+8, 6) || !p.sameGround(s.Pos, here) {
			continue
		}
		contested := false
		for _, c := range p.contacts {
			if c.Drone || p.t-c.ViewT > 5 {
				continue
			}
			if dc := gap2(c.Pos[0], c.Pos[1], s.Pos[0], s.Pos[1]); dc < 30 || dc < 0.8*d {
				contested = true
			}
		}
		if contested {
			continue
		}
		if sc := armWorth(s.Kind) / (d + 15); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	return best
}
