package main

// Legs: route following and the short-horizon rollout that picks each tick's move. Every
// candidate heading is simulated for most of a second and charged for the bullets, blasts,
// sight lines and zone edge it would run into.

import "math"

type Drive struct {
	Kind    byte // 'h' hold, 'g' go to a point, 's' strafe around a contact
	GX, GY  float64
	Around  *Contact
	RMin    float64
	RMax    float64
	Weight  float64  // pull of the objective, per metre
	Sprint  bool     // boosting is welcome
	Still   bool     // prefer standing when nothing else matters
	Caution float64  // multiplier on the cost of being seen by threats
	Seek    *Contact // we want a firing line to this one
	Crawl   float64  // 0 or a speed cap fraction for silent walking
	Pet     int      // drone id we are deliberately fighting (-1 none)
}

type inbound struct {
	pos, vel [3]float64
	until    float64 // seconds until it is spent
	dmg      float64
	blast    bool
	radius   float64
}

func (p *Pilot) zoneNow(ahead float64) (cx, cy, r float64) {
	z := &p.f.Zone
	t := p.t + ahead
	k := 0.0
	if z.ShrinkEnd > z.ShrinkStart {
		k = clamp((t-z.ShrinkStart)/(z.ShrinkEnd-z.ShrinkStart), 0, 1)
	} else if t >= z.ShrinkEnd {
		k = 1
	}
	return z.Center[0] + (z.NextCenter[0]-z.Center[0])*k, z.Center[1] + (z.NextCenter[1]-z.Center[1])*k,
		z.Radius + (z.NextRadius-z.Radius)*k
}

func (p *Pilot) zoneToll(k int) float32 {
	x, y := p.L.XY(k)
	cx, cy, r := p.zoneNow(2)
	if gap2(x, y, cx, cy) > r-3 {
		return 6
	}
	return 0
}

// heading returns the unit direction to walk in order to reach (gx,gy), using the router.
func (p *Pilot) heading(gx, gy float64) (dx, dy float64, ok bool) {
	d := gap2(gx, gy, p.x, p.y)
	if d < 1 {
		return 0, 0, true
	}
	straight := func() (float64, float64, bool) { return (gx - p.x) / d, (gy - p.y) / d, true }
	here := p.L.Snap(p.x, p.y, p.z, 3)
	if here < 0 || !p.me.Grounded && p.z > float64(p.L.Lvl[here])+3.5 {
		return straight()
	}
	if d < 14 && p.L.Walkable(p.x, p.y, gx, gy) {
		return straight()
	}
	stale := len(p.path) == 0 || gap2(gx, gy, p.pathGoal[0], p.pathGoal[1]) > 5 || p.t-p.pathT > 1.6
	if !stale && p.pathIdx < len(p.path) {
		nx, ny := p.L.XY(int(p.path[p.pathIdx]))
		stale = gap2(nx, ny, p.x, p.y) > 12
	}
	wait := 0.35
	if !p.pathOK {
		wait = 1.0
	}
	if stale && p.t-p.pathT >= wait {
		goal := p.L.Snap(gx, gy, math.NaN(), 5)
		p.path, p.pathOK = p.L.Route(here, goal, 30000, p.zoneToll)
		p.pathIdx = 0
		p.pathGoal = [2]float64{gx, gy}
		p.pathT = p.t
	}
	if len(p.path) == 0 {
		return (gx - p.x) / d, (gy - p.y) / d, false
	}
	for p.pathIdx < len(p.path)-1 {
		nx, ny := p.L.XY(int(p.path[p.pathIdx]))
		if gap2(nx, ny, p.x, p.y) > 3.2 {
			break
		}
		p.pathIdx++
	}
	// pull the string: head for the farthest node we can walk straight to
	tgt := p.pathIdx
	for look := min(p.pathIdx+12, len(p.path)-1); look > p.pathIdx; look -= 2 {
		nx, ny := p.L.XY(int(p.path[look]))
		if p.L.Walkable(p.x, p.y, nx, ny) {
			tgt = look
			break
		}
	}
	if tgt > p.pathIdx+1 {
		p.pathIdx = tgt - 1
	}
	tx, ty := p.L.XY(int(p.path[tgt]))
	if tgt == len(p.path)-1 && p.pathOK && gap2(tx, ty, gx, gy) < 5 {
		tx, ty = gx, gy
	}
	td := gap2(tx, ty, p.x, p.y)
	if td < 0.5 {
		return straight()
	}
	ok = p.pathOK || gap2(tx, ty, p.x, p.y) > 4 || tgt < len(p.path)-1
	return (tx - p.x) / td, (ty - p.y) / td, ok
}

func (p *Pilot) gatherInbound() []inbound {
	var out []inbound
	w := &p.R.Weapons
	for i := range p.f.Shots {
		s := &p.f.Shots[i]
		sp := math.Sqrt(s.Vel[0]*s.Vel[0] + s.Vel[1]*s.Vel[1] + s.Vel[2]*s.Vel[2])
		if sp < 1 {
			continue
		}
		if s.Kind == "mortar" {
			// fly the shell forward until it lands
			pos, vel := s.Pos, s.Vel
			g := w.Mortar.Gravity
			const h = 0.15
			tt := 0.0
			for tt < 2.6 {
				nx, ny := pos[0]+vel[0]*h, pos[1]+vel[1]*h
				nz := pos[2] + vel[2]*h - 0.5*g*h*h
				if !p.A.Clear(pos[0], pos[1], pos[2], nx, ny, nz) || nz < p.A.DeckAt(nx, ny) {
					break
				}
				pos = [3]float64{nx, ny, nz}
				vel[2] -= g * h
				tt += h
			}
			rad := w.Mortar.SplashRadius * 1.3
			if tt < 2.6 && gap2(pos[0], pos[1], p.x, p.y) < rad+p.bodyR+40 {
				out = append(out, inbound{pos: pos, until: tt + h, dmg: w.Mortar.Splash, blast: true, radius: rad})
			}
			continue
		}
		rx, ry := p.x-s.Pos[0], p.y-s.Pos[1]
		vh := math.Hypot(s.Vel[0], s.Vel[1])
		d := math.Hypot(rx, ry)
		if (rx*s.Vel[0]+ry*s.Vel[1])/sp < -3 || d > 110 {
			continue
		}
		if math.Abs(rx*s.Vel[1]-ry*s.Vel[0])/math.Max(vh, 1) > 5+p.bodyR {
			continue
		}
		reach := p.A.Reach(s.Pos[0], s.Pos[1], s.Pos[2], s.Vel[0]/sp, s.Vel[1]/sp, s.Vel[2]/sp, d+20)
		if reach < d-p.bodyR-3 {
			continue
		}
		dmg := w.Gun.Damage
		if s.Kind == "npc" {
			dmg = p.R.Npc.Damage
		}
		out = append(out, inbound{pos: s.Pos, vel: s.Vel, until: reach / sp, dmg: dmg})
	}
	return out
}

func segGap(px, py, ax, ay, bx, by float64) (float64, float64) {
	ex, ey := bx-ax, by-ay
	l2 := ex*ex + ey*ey
	u := 0.0
	if l2 > 1e-12 {
		u = clamp(((px-ax)*ex+(py-ay)*ey)/l2, 0, 1)
	}
	return math.Hypot(px-ax-ex*u, py-ay-ey*u), u
}

// steer fills in move and boost for this tick.
func (p *Pilot) steer(o *Order, dv *Drive) {
	me := p.me
	var wx, wy float64 // where the objective pulls us
	weight := dv.Weight
	switch dv.Kind {
	case 'g':
		wx, wy, _ = p.heading(dv.GX, dv.GY)
	case 's':
		c := dv.Around
		if c == nil {
			break
		}
		rx, ry := c.Pos[0]-p.x, c.Pos[1]-p.y
		d := math.Hypot(rx, ry)
		if d < 1e-6 {
			break
		}
		rx, ry = rx/d, ry/d
		if !c.Sight && dv.Seek == c {
			// no firing line: walk toward it along the routed path
			wx, wy, _ = p.heading(c.Pos[0], c.Pos[1])
			weight *= 0.75
			break
		}
		if p.t >= p.strafeFlip {
			if p.rng.Float64() < 0.75 {
				p.strafeSign = -p.strafeSign
			}
			p.strafeFlip = p.t + knob("flipLo", 0.12) + knob("flipSpan", 0.2)*p.rng.Float64()
		}
		radial := 0.0
		if d > dv.RMax {
			radial = clamp((d-dv.RMax)/15, 0, 1)
		} else if d < dv.RMin {
			radial = -clamp((dv.RMin-d)/15, 0, 1)
		}
		tx, ty := -ry*p.strafeSign, rx*p.strafeSign
		k := 1 - 0.6*math.Abs(radial)
		wx, wy = tx*k+rx*radial, ty*k+ry*radial
		if n := math.Hypot(wx, wy); n > 1e-6 {
			wx, wy = wx/n, wy/n
		}
	}

	shells := p.gatherInbound()
	canSprint := me.Energy > 12 && me.Grounded && (dv.Sprint || (len(shells) > 0 && me.Energy > knob("dodgeEn", 35)))

	// threats whose sight lines we would rather not stand in
	type watcher struct {
		c     *Contact
		dps   float64
		mz    float64
		laser bool
	}
	var eyes [5]watcher
	nEyes := 0
	for _, c := range p.contacts {
		if c.Drone || c.Ghost || p.t-c.ViewT > 3 || c.D > 260 {
			continue
		}
		if nEyes == len(eyes) {
			// replace the farthest
			far := 0
			for i := 1; i < nEyes; i++ {
				if eyes[i].c.D > eyes[far].c.D {
					far = i
				}
			}
			if eyes[far].c.D <= c.D {
				continue
			}
			eyes[far] = watcher{c: c}
			continue
		}
		eyes[nEyes] = watcher{c: c}
		nEyes++
	}
	for i := 0; i < nEyes; i++ {
		c := eyes[i].c
		eyes[i].dps = p.intakeFrom(c, c.D)
		eyes[i].mz = c.Pos[2] + 7*c.scale(&p.R)
		eyes[i].laser = c.Laser && c.Charging && c.Sight && p.t-c.AimT < 0.3
	}
	caution := dv.Caution
	boltReach := p.R.Weapons.Lightning.Range + 8
	haveBolt := p.alt != nil && p.alt.Kind == "lightning"

	startNode := p.L.Near(p.x, p.y)
	collide := me.Grounded && p.L.Open[startNode] && math.Abs(float64(p.L.Lvl[startNode])-p.z) < 3
	accel := p.R.Mech.GroundAccel
	if !me.Grounded {
		accel = p.R.Mech.AirAccel
	}
	slow := 1.0
	if p.alt != nil && p.alt.Kind == "laser" && p.alt.Charge > 0 && me.Stats.ChargeMove > 0 {
		slow = me.Stats.ChargeMove
	}
	const steps, h = 8, 0.1
	horizon := float64(steps) * h
	zcx, zcy, zr := p.zoneNow(horizon)
	_, _, zr0 := p.zoneNow(0)
	z0x, z0y, _ := p.zoneNow(0)
	outNow := gap2(p.x, p.y, z0x, z0y) - zr0

	bestScore := math.Inf(-1)
	bestX, bestY, bestBoost := 0.0, 0.0, false
	try := func(dx, dy float64, sprint bool) {
		speed := p.walk
		if sprint {
			speed = p.sprint
		}
		speed *= slow
		if dv.Crawl > 0 && !sprint {
			speed *= dv.Crawl
		}
		x, y := p.x, p.y
		vx, vy := me.Vel[0], me.Vel[1]
		score := 0.0
		blocked := false
		var px6, py6 float64
		lvl := float64(p.L.Lvl[startNode])
		for s := 1; s <= steps; s++ {
			tx, ty := dx*speed, dy*speed
			ex, ey := tx-vx, ty-vy
			if n := math.Hypot(ex, ey); n > accel*h {
				ex, ey = ex/n*accel*h, ey/n*accel*h
			}
			vx, vy = vx+ex, vy+ey
			nx, ny := x+vx*h, y+vy*h
			if collide {
				k := p.L.Near(nx, ny)
				if k != startNode {
					rise := float64(p.L.Lvl[k]) - lvl
					if !p.L.Open[k] || rise > 2.6 || rise < -6 {
						blocked = true
						vx, vy = 0, 0
						nx, ny = x, y
					} else {
						lvl = float64(p.L.Lvl[k])
					}
				}
			}
			x, y = nx, ny
			t0, t1 := float64(s-1)*h, float64(s)*h
			for i := range shells {
				b := &shells[i]
				if b.blast {
					if b.until > t0 && (b.until <= t1 || s == steps) {
						d := gap2(x, y, b.pos[0], b.pos[1])
						reach := b.radius + p.bodyR
						if d < reach && math.Abs(b.pos[2]-(p.z+p.bodyH/2)) < reach+p.bodyH/2 {
							score -= b.dmg*(1-0.7*d/reach) + 40
						}
					}
					continue
				}
				if t0 >= b.until {
					continue
				}
				te := math.Min(t1, b.until)
				d, u := segGap(x, y, b.pos[0]+b.vel[0]*t0, b.pos[1]+b.vel[1]*t0, b.pos[0]+b.vel[0]*te, b.pos[1]+b.vel[1]*te)
				bz := b.pos[2] + b.vel[2]*(t0+(te-t0)*u)
				if d < p.bodyR+0.85 && bz > p.z-0.6 && bz < p.z+p.bodyH+0.6 {
					score -= knob("dodge", 1.0) * b.dmg
				}
			}
			if s == 6 {
				px6, py6 = x, y
			}
		}
		moving := dx != 0 || dy != 0
		score += ((px6-p.x)*wx + (py6-p.y)*wy) * weight
		if blocked {
			score -= 3
		}
		if moving {
			score += 0.25 * (dx*p.lastMove[0] + dy*p.lastMove[1])
		} else if dv.Still {
			score += 0.6
		}
		if sprint && !dv.Sprint {
			score -= knob("sprintTax", 1.5)
		}
		// zone edge
		out := gap2(x, y, zcx, zcy) - zr
		if out > -4 {
			if outNow < -0.5 {
				score -= (out + 4) * 8
			} else {
				score -= (out - outNow) * 6
			}
		}
		// sight lines
		myMid := p.z + p.bodyH/2
		for i := 0; i < nEyes; i++ {
			e := &eyes[i]
			c := e.c
			d := gap2(x, y, c.Pos[0], c.Pos[1])
			lit := p.A.Clear(c.Pos[0], c.Pos[1], e.mz, x, y, myMid)
			if e.laser && p.A.Clear(c.Pos[0], c.Pos[1], e.mz, px6, py6, myMid) {
				score -= 0.25 * p.R.Weapons.Laser.Damage * p.cubeGain(c.Cubes)
			}
			if lit {
				mult := caution
				if dv.Seek != nil && dv.Seek != c && !dv.Seek.Drone {
					score -= knob("isoW", 0)
				}
				if dv.Seek == c {
					mult *= 0.2
					score += knob("seekW", 4)
				}
				align := 0.45
				if p.t-c.AimT < 0.5 || p.t-c.StruckT < 2 {
					align = 1
				}
				score -= mult * knob("expo", 0.05) * e.dps * align
			}
			if c.Bolt && !haveBolt && d < boltReach {
				score -= (boltReach - d) * knob("bolt", 0.7)
			}
		}
		for _, c := range p.contacts {
			if !c.Drone || c.ID == dv.Pet || p.t-c.ViewT > 4 {
				continue
			}
			lim := p.R.Npc.Aggro + 6
			if d := gap2(x, y, c.Pos[0], c.Pos[1]); d < lim {
				score -= (lim - d) * 0.35
			}
		}
		if score > bestScore {
			bestScore, bestX, bestY, bestBoost = score, dx, dy, sprint
		}
	}

	try(0, 0, false)
	for i := 0; i < 16; i++ {
		a := float64(i) * math.Pi / 8
		try(math.Cos(a), math.Sin(a), false)
		if canSprint && i%2 == 0 {
			try(math.Cos(a), math.Sin(a), true)
		}
	}
	if wx != 0 || wy != 0 {
		try(wx, wy, false)
		if canSprint {
			try(wx, wy, true)
		}
	}
	if dv.Crawl > 0 && !bestBoost {
		bestX, bestY = bestX*dv.Crawl, bestY*dv.Crawl
	}
	o.MoveX, o.MoveY, o.Boost = bestX, bestY, bestBoost

	p.shake(o, dv.Kind == 'g' && (wx != 0 || wy != 0))
	p.lastMove = [2]float64{o.MoveX, o.MoveY}
	if n := math.Hypot(o.MoveX, o.MoveY); n > 1e-6 {
		p.lastMove = [2]float64{o.MoveX / n, o.MoveY / n}
	}
}

// shake frees the mech when it is trying to travel and getting nowhere.
func (p *Pilot) shake(o *Order, travelling bool) {
	if !travelling {
		p.stuckFor, p.stuckT, p.stuckPos = 0, p.t, [2]float64{p.x, p.y}
		return
	}
	if p.t < p.shakeT {
		o.MoveX, o.MoveY = 0.4*o.MoveX+p.shakeDir[0], 0.4*o.MoveY+p.shakeDir[1]
		if p.me.Grounded {
			o.Jump = true
		} else if p.me.Energy > 30 {
			o.Boost = true
		}
		return
	}
	if p.t-p.stuckT >= 0.5 {
		if math.Hypot(o.MoveX, o.MoveY) > 0.3 && gap2(p.x, p.y, p.stuckPos[0], p.stuckPos[1]) < 1.2 {
			p.stuckFor += p.t - p.stuckT
		} else {
			p.stuckFor = 0
		}
		p.stuckT, p.stuckPos = p.t, [2]float64{p.x, p.y}
	}
	if p.stuckFor >= 1.0 {
		a := p.rng.Float64() * 2 * math.Pi
		p.shakeDir = [2]float64{math.Cos(a), math.Sin(a)}
		p.shakeT = p.t + 0.7
		p.stuckFor = 0
		p.path = nil
	}
}
