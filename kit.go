package main

// Outfitting: upgrade picks, weapon pickups, and choosing what loot is worth walking to.

import "math"

// Upgrade preference, most wanted first: stay alive, then hit harder, then everything else.
var upgradeOrder = []string{
	"plating", "armor", "nanites", "gun_damage", "gun_rate", "laser_damage", "laser_charge",
	"servos", "gun_mag", "lightning_efficiency", "gyros", "gun_velocity", "reactor", "capacitor",
	"jets", "lightning_chain", "lightning_range", "sensors", "laser_pierce",
	"mortar_reload", "mortar_splash", "mortar_velocity",
}

func (p *Pilot) pickUpgrade(o *Order) {
	if p.f.Offer == nil || len(p.f.Offer.Choices) == 0 {
		return
	}
	best, bestRank := 0, len(upgradeOrder)+1
	for i, ch := range p.f.Offer.Choices {
		rank := len(upgradeOrder)
		for r, id := range upgradeOrder {
			if id == ch.ID {
				rank = r
				break
			}
		}
		if rank < bestRank {
			best, bestRank = i, rank
		}
	}
	if best > 2 {
		best = 0
	}
	o.Pick = best
}

// armWorth ranks secondary weapons. The laser is the prize; a mortar is left on the ground
// because carrying one makes every enemy laser hit harder.
func armWorth(kind string) float64 {
	switch kind {
	case "laser":
		return 9
	case "lightning":
		return knob("wBolt", 4)
	case "mortar":
		return knob("wMortar", 0)
	}
	return 0
}

func (p *Pilot) wantsArm(kind string) bool {
	v := armWorth(kind)
	if v <= 0 {
		return false
	}
	if p.alt == nil {
		return true
	}
	if p.alt.Kind == kind || (p.alt.Kind == "laser" && p.alt.Charge > 0) {
		return false
	}
	return v > armWorth(p.alt.Kind)+0.5
}

// grabArms issues take/drop when we are standing on a weapon we want.
func (p *Pilot) grabArms(o *Order) {
	if p.t-p.takeT < 0.25 {
		return
	}
	reach := p.R.Loot.PickupRadius
	if reach <= 0 {
		reach = 4
	}
	reach -= 0.3
	bestID, bestV := -1, 0.0
	for _, s := range p.stash {
		if s.Class != 'w' || s.SeenT != p.t || !p.wantsArm(s.Kind) {
			continue
		}
		if gap2(s.Pos[0], s.Pos[1], p.x, p.y) > reach || math.Abs(s.Pos[2]-p.z) > 3.7 {
			continue
		}
		if v := armWorth(s.Kind); v > bestV {
			bestID, bestV = s.ID, v
		}
	}
	if bestID < 0 {
		return
	}
	o.Take = bestID
	if p.alt != nil {
		o.Drop = p.altSlot
	}
	p.takeT = p.t
}

// Prize is a loot objective.
type Prize struct {
	ID    int
	Class byte // 'c' cube, 'k' crate, 'w' weapon, 'd' drone, 's' supply drop
	Pos   [3]float64
	Kind  string
	Drone *Contact
	Item  *Stash
}

// insideLater: will the point still be inside the circle after eta seconds, with margin?
func (p *Pilot) insideLater(x, y, eta, margin float64) bool {
	cx, cy, r := p.zoneNow(eta)
	return gap2(x, y, cx, cy) < r-margin
}

func (p *Pilot) sameGround(pos [3]float64, here int) bool {
	k := p.L.Snap(pos[0], pos[1], pos[2], 2)
	return k >= 0 && here >= 0 && p.L.Region[k] == p.L.Region[here] && math.Abs(float64(p.L.Lvl[k])-pos[2]) < 4
}

// choosePrize picks the best loot objective, or nil.
func (p *Pilot) choosePrize(wantCubes int, danger, shy bool, maxD float64) *Prize {
	here := p.L.Snap(p.x, p.y, p.z, 3)
	var best *Prize
	bestScore := 0.0
	consider := func(pr Prize, value, handling float64) {
		if until, bad := p.banned[pr.ID]; bad && p.t < until {
			return
		}
		d := gap2(pr.Pos[0], pr.Pos[1], p.x, p.y)
		if d > maxD {
			return
		}
		tt := 1.25*d/p.walk + handling
		if !p.insideLater(pr.Pos[0], pr.Pos[1], tt+12, 6) {
			return
		}
		if pr.Class != 's' && !p.sameGround(pr.Pos, here) {
			return
		}
		score := value / (tt + 1.5)
		if pr.ID == p.lootID {
			score *= 1.25
		}
		for _, c := range p.contacts {
			if c.Drone || p.t-c.ViewT > 5 {
				continue
			}
			dc := gap2(c.Pos[0], c.Pos[1], pr.Pos[0], pr.Pos[1])
			if pr.Class != 'c' && dc < 45 {
				score *= 0.35
			}
			if pr.Class == 'c' && c.Bolt && dc < p.R.Weapons.Lightning.Range+8 && p.t-c.ViewT < 4 {
				score *= 0.2
			}
			if shy && p.t-c.ViewT < 4 && c.D < 230 &&
				p.A.Clear(c.Pos[0], c.Pos[1], c.Pos[2]+8, pr.Pos[0], pr.Pos[1], pr.Pos[2]+4.5) {
				score *= 0.5
			}
		}
		if score > bestScore {
			bestScore = score
			cp := pr
			best = &cp
		}
	}
	needCubes := p.me.Cubes < wantCubes
	for _, s := range p.stash {
		switch s.Class {
		case 'w':
			if p.wantsArm(s.Kind) {
				consider(Prize{ID: s.ID, Class: 'w', Pos: s.Pos, Kind: s.Kind, Item: s}, 1.4*armWorth(s.Kind), 0.5)
			}
		case 'c':
			if needCubes {
				consider(Prize{ID: s.ID, Class: 'c', Pos: s.Pos, Item: s}, 11, 0)
			}
		case 'k':
			if needCubes && !danger {
				consider(Prize{ID: s.ID, Class: 'k', Pos: s.Pos, Item: s}, 7, 2.5)
			}
		}
	}
	if needCubes && !danger && p.hpf > 0.6 {
		for _, c := range p.contacts {
			if c.Drone && p.t-c.ViewT < 20 {
				consider(Prize{ID: c.ID, Class: 'd', Pos: c.Pos, Drone: c}, 11, 5)
			}
		}
	}
	for i := range p.f.Drops {
		dr := &p.f.Drops[i]
		d := gap2(dr.Pos[0], dr.Pos[1], p.x, p.y)
		if d > 300 {
			continue
		}
		if dr.LandsIn > 1.3*d/p.walk+6 && p.lootID != dr.ID {
			continue
		}
		if shy && p.me.Cubes >= wantCubes && p.hpf > 0.7 {
			continue
		}
		v := 30.0
		if p.hpf < 0.6 {
			v *= 1.5
		}
		save := maxD
		maxD = 300
		consider(Prize{ID: dr.ID, Class: 's', Pos: dr.Pos}, v, 4+0.3*dr.LandsIn)
		maxD = save
	}
	return best
}

// nearCubes finds a cube (or pile) worth a short detour in the middle of a fight.
func (p *Pilot) nearCubes(wantCubes int) *Stash {
	if p.me.Cubes >= wantCubes+6 {
		return nil
	}
	here := p.L.Snap(p.x, p.y, p.z, 3)
	var best *Stash
	bestScore := 0.0
	for _, s := range p.stash {
		if s.Class != 'c' {
			continue
		}
		if until, bad := p.banned[s.ID]; bad && p.t < until {
			continue
		}
		d := gap2(s.Pos[0], s.Pos[1], p.x, p.y)
		n := 0
		for _, q := range p.stash {
			if q.Class == 'c' && gap2(q.Pos[0], q.Pos[1], s.Pos[0], s.Pos[1]) < 7 {
				n++
			}
		}
		reach := 40.0
		if n >= 2 {
			reach = 110
		}
		if d > reach || (d > 30 && p.hpf < 0.3) || !p.insideLater(s.Pos[0], s.Pos[1], d/p.walk+3, 4) || !p.sameGround(s.Pos, here) {
			continue
		}
		risky := false
		for _, c := range p.contacts {
			if !c.Drone && c.Bolt && p.t-c.ViewT < 4 && gap2(c.Pos[0], c.Pos[1], s.Pos[0], s.Pos[1]) < p.R.Weapons.Lightning.Range+8 {
				risky = true
			}
		}
		if risky {
			continue
		}
		if sc := float64(n) / (d + 12); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	return best
}
