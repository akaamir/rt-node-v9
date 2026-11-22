package main

// Weapons: firing solutions, the damage-exchange estimate used to price fights, and trigger
// discipline for the four weapon kinds.

import "math"

const lagS = 0.075 // our order lands about a tick and a half after the snapshot

// landRate estimates the fraction of arm-gun rounds that connect at a range.
func landRate(d float64, ours bool) float64 {
	r := 0.1 + 0.86/(1+math.Exp((d-100)/28))
	if !ours {
		r *= 0.95
	}
	return r
}

// outputAt: our sustained damage per second against a target at range d.
func (p *Pilot) outputAt(d float64, drone bool) float64 {
	w := &p.R.Weapons
	dps := 0.0
	if g := p.gun; g != nil {
		st := g.Stats
		if st.Cooldown > 0 && st.Mag > 0 && d < st.Range {
			dps += st.Damage * st.Mag / (st.Mag*st.Cooldown + st.Reload) * landRate(d, true)
		}
	}
	if a := p.alt; a != nil {
		st := a.Stats
		switch a.Kind {
		case "laser":
			if d < st.Range {
				cyc := st.Charge + st.Cooldown
				if cyc <= 0 {
					cyc = w.Laser.Charge + w.Laser.Cooldown
				}
				dps += st.Damage / math.Max(cyc, 0.5) * 0.75
			}
		case "lightning":
			if d < st.Range-2 && p.me.Energy > 15 && st.Cooldown > 0 {
				dps += st.Damage / st.Cooldown
			}
		case "mortar":
			if d > 12 && d < 200 && st.Cooldown > 0 && !drone {
				dps += st.Damage / st.Cooldown * 0.25
			}
		}
	}
	return dps * p.cubeGain(p.me.Cubes)
}

// intakeFrom: damage per second a contact can pour into us at range d.
func (p *Pilot) intakeFrom(c *Contact, d float64) float64 {
	w := &p.R.Weapons
	if c.Drone {
		if d > 95 {
			return 0
		}
		return p.R.Npc.Damage * 0.5
	}
	dps := 0.0
	if d < w.Gun.Range && w.Gun.Cooldown > 0 {
		dps += w.Gun.Damage * w.Gun.Mag / (w.Gun.Mag*w.Gun.Cooldown + w.Gun.Reload) * landRate(d, false)
	}
	if c.Laser && d < w.Laser.Range {
		shorted := p.alt != nil && p.alt.Kind == "lightning" && d < p.alt.Stats.Range-3 && p.me.Energy > 20
		if !shorted {
			l := w.Laser.Damage / math.Max(w.Laser.Charge+w.Laser.Cooldown, 0.5) * 0.75
			if p.alt != nil && p.alt.Kind == "mortar" {
				l *= 1 + w.Laser.Cookoff
			}
			dps += l
		}
	}
	if c.Bolt && d < w.Lightning.Range+6 && w.Lightning.Cooldown > 0 {
		dps += w.Lightning.Damage / w.Lightning.Cooldown
	}
	if c.Mortar && d > 12 && d < 200 && w.Mortar.Cooldown > 0 {
		dps += w.Mortar.Damage / w.Mortar.Cooldown * 0.25
	}
	return dps * p.cubeGain(c.Cubes) * (1 - clamp(p.me.Stats.Armor, 0, 0.8))
}

// edge prices a duel with one contact: (time we last) / (time it lasts). Above 1 favours us.
func (p *Pilot) edge(c *Contact) float64 {
	best := 0.0
	for _, r := range [3]float64{c.D, 26, 40} {
		in := p.intakeFrom(c, r)
		out := p.outputAt(r, c.Drone)
		if out < 1 {
			continue
		}
		hp, foe := p.me.HP, math.Max(c.HP, 20)
		if math.Abs(r-c.D) > 8 {
			// closing or opening the range costs time under fire at the midpoint
			mid := (r + c.D) / 2
			secs := math.Abs(r-c.D) / 9 * 0.8
			hp -= p.intakeFrom(c, mid) * secs
			foe = math.Max(foe-p.outputAt(mid, c.Drone)*secs, 20)
			if hp <= 0 {
				continue
			}
		}
		v := 10.0
		if in >= 1 {
			v = (hp / in) / (foe / out)
		}
		if r == c.D {
			v *= 1.05
		}
		if v > best {
			best = v
		}
	}
	return math.Min(best, 10)
}

// Shot is a firing solution.
type Shot struct {
	Yaw, Pitch float64
	Dist       float64
	Open       bool // nothing solid along the line
	OK         bool
	Flight     float64
}

func (p *Pilot) torsoErr(s *Shot) (float64, float64) {
	// credit the slew the torso will make before the order takes effect
	ey := math.Abs(turn(s.Yaw-p.me.Aim[0])) - 0.9*p.me.Stats.TurnRate*0.05
	ep := math.Abs(s.Pitch-p.me.Aim[1]) - 0.9*p.me.Stats.PitchRate*0.05
	return math.Max(ey, 0), math.Max(ep, 0)
}

// straight solves a flat-trajectory shot (speed<=0 means instant) at a point moving at vel.
func (p *Pilot) straight(tp, tv [3]float64, aimZ, speed, slack float64) Shot {
	ox := p.x + p.me.Vel[0]*lagS
	oy := p.y + p.me.Vel[1]*lagS
	oz := p.z + p.muzzleH
	if !p.me.Grounded {
		oz += p.me.Vel[2] * lagS
	}
	var s Shot
	tf := 0.0
	var ax, ay, az float64
	for it := 0; it < 3; it++ {
		// trust the target's velocity less the longer the round is in the air
		keep := 1.0
		if tf > 0.2 {
			keep = math.Max(knob("leadMin", 0.55), 1-(tf-0.2)*knob("leadFade", 1.1))
		}
		if p.leadPick >= 0 && speed > 0 {
			keep = p.leadPick
		}
		lead := lagS + tf*keep
		ax, ay, az = tp[0]+tv[0]*lead, tp[1]+tv[1]*lead, tp[2]+aimZ
		d := math.Sqrt((ax-ox)*(ax-ox) + (ay-oy)*(ay-oy) + (az-oz)*(az-oz))
		s.Dist = d
		if speed <= 0 {
			break
		}
		tf = math.Max(d-3, 0) / speed
	}
	s.Flight = tf
	dx, dy, dz := ax-ox, ay-oy, az-oz
	h := math.Hypot(dx, dy)
	s.Yaw = math.Atan2(dy, dx)
	s.Pitch = math.Atan2(dz, math.Max(h, 1e-6))
	if s.Dist > 1e-6 {
		lim := s.Dist - slack
		s.Open = lim <= 0 || p.A.Reach(ox, oy, oz, dx/s.Dist, dy/s.Dist, dz/s.Dist, lim) >= lim
	}
	s.OK = true
	return s
}

// lob solves a mortar arc onto the ground under a moving target.
func (p *Pilot) lob(tp, tv [3]float64, st *ArmStats) Shot {
	v, g := st.Speed, st.Gravity
	if v <= 0 {
		v = p.R.Weapons.Mortar.Speed
	}
	if g <= 0 {
		g = p.R.Weapons.Mortar.Gravity
	}
	ttl := p.R.Weapons.Mortar.TTL
	if ttl <= 0 {
		ttl = 7
	}
	ox, oy := p.x+p.me.Vel[0]*lagS, p.y+p.me.Vel[1]*lagS
	oz := p.z + p.bodyH + 0.5
	for arc := 0; arc < 2; arc++ {
		tf, th, ok := 0.6, 0.0, true
		var px, py, pz, d float64
		for it := 0; it < 5 && ok; it++ {
			px, py = tp[0]+tv[0]*(lagS+tf), tp[1]+tv[1]*(lagS+tf)
			pz = p.A.FootAt(px, py)
			if pz > tp[2]+3 {
				pz = tp[2]
			}
			pz += 0.3
			d = gap2(px, py, ox, oy)
			h := pz - oz
			disc := v*v*v*v - g*(g*d*d+2*h*v*v)
			if d < 1 || disc < 0 {
				ok = false
				break
			}
			root := math.Sqrt(disc)
			if arc == 0 {
				th = math.Atan2(v*v-root, g*d)
			} else {
				th = math.Atan2(v*v+root, g*d)
			}
			if math.Cos(th) < 0.05 {
				ok = false
				break
			}
			tf = d / (v * math.Cos(th))
		}
		if !ok || th < p.me.Stats.MinPitch || th > p.me.Stats.MaxPitch || tf > ttl-0.3 {
			continue
		}
		// walk the arc and make sure nothing is in the way before the target
		yaw := math.Atan2(py-oy, px-ox)
		cx, cy := math.Cos(yaw), math.Sin(yaw)
		vh, vz := v*math.Cos(th), v*math.Sin(th)
		clear := true
		n := int(tf/0.12) + 2
		lx, ly, lz := ox+cx*3, oy+cy*3, oz+math.Tan(th)*3
		for i := 1; i <= n && clear; i++ {
			tt := tf * float64(i) / float64(n)
			nx, ny, nz := ox+cx*vh*tt, oy+cy*vh*tt, oz+vz*tt-0.5*g*tt*tt
			if gap2(nx, ny, ox, oy) > 3 && !p.A.Clear(lx, ly, lz, nx, ny, nz) && gap2(nx, ny, px, py) > 3.5 {
				clear = false
			}
			if gap2(nx, ny, ox, oy) > 3 {
				lx, ly, lz = nx, ny, nz
			}
		}
		if clear {
			return Shot{Yaw: yaw, Pitch: th, Dist: d, Open: true, OK: true, Flight: tf}
		}
	}
	return Shot{}
}

func (p *Pilot) beamDamage(charge float64, st *ArmStats) float64 {
	full := st.Charge
	if full <= 0 {
		full = 0.75
	}
	return st.Damage * (st.MinFrac + (1-st.MinFrac)*clamp(charge/full, 0, 1)) * p.cubeGain(p.me.Cubes)
}

// Mark is whatever we are pointing weapons at this tick.
type Mark struct {
	Lead     float64 // velocity fraction to lead by (-1 = default schedule)
	Pos, Vel [3]float64
	Radius   float64
	MidZ     float64
	HP       float64
	Mech     bool
	Mortar   bool
	Sight    bool // line of sight right now
	Recent   bool // had line of sight a moment ago
}

func (p *Pilot) markOf(c *Contact) Mark {
	m := Mark{Pos: c.Pos, Vel: c.Vel, HP: c.HP, Mech: !c.Drone, Mortar: c.Mortar,
		Sight: c.Sight && c.Listed, Recent: p.t-c.SightT < 0.8}
	m.Lead = -1
	if c.Drone {
		m.Radius, m.MidZ = 1.8, 1.75
	} else {
		m.Lead = p.bestLead(c)
		s := c.scale(&p.R)
		m.Radius, m.MidZ = 2.5*s, 4.5*s
	}
	return m
}

// shoot works the torso and triggers against a mark. permit=false keeps the guns quiet but
// still lets an already-charged laser finish its shot.
func (p *Pilot) shoot(o *Order, m *Mark, permit bool) {
	p.leadPick = m.Lead
	defer func() { p.leadPick = -1 }()
	var gunS, rayS, arcS Shot
	rayS = p.straight(m.Pos, m.Vel, m.MidZ, 0, 0.9*m.Radius)
	if p.gun != nil {
		gunS = p.straight(m.Pos, m.Vel, m.MidZ, p.gun.Stats.Speed, 0.9*m.Radius)
	}
	aim := rayS
	if gunS.OK {
		aim = gunS
	}
	usingArc := false
	alt := p.alt
	laserBusy := false
	if alt != nil && alt.Kind == "laser" && alt.Charge > 0 {
		full := alt.Stats.Charge
		if alt.Charge >= full-0.2 || p.beamDamage(alt.Charge+0.1, &alt.Stats) >= m.HP {
			aim = rayS
			laserBusy = true
		}
	}
	if alt != nil && alt.Kind == "mortar" && permit && m.Mech && alt.Cooldown <= 0.15 {
		arcS = p.lob(m.Pos, m.Vel, &alt.Stats)
		if arcS.OK {
			minSafe := alt.Stats.SplashRadius + p.bodyR + 3
			drift := math.Hypot(m.Vel[0], m.Vel[1]) * arcS.Flight * 0.5
			worth := arcS.Dist > minSafe && drift < 0.75*alt.Stats.SplashRadius
			gunIdle := p.gun == nil || p.gun.Ammo <= 0 || p.gun.Reload > 0 || !gunS.Open || !m.Sight
			if arcS.Dist > minSafe && (worth || (gunIdle && arcS.Dist > 25)) {
				aim = arcS
				usingArc = true
			}
		}
	}
	o.Yaw = aim.Yaw
	o.Pitch = clamp(aim.Pitch, p.me.Stats.MinPitch, p.me.Stats.MaxPitch)

	fire := [2]bool{}
	// arm gun
	if g := p.gun; g != nil && gunS.OK {
		if g.Ammo <= 0 && g.Reload <= 0 {
			o.Reload = true
		}
		reach := g.Stats.Range - 5
		if !m.Mech {
			reach = math.Min(reach, 120)
		}
		ey, ep := p.torsoErr(&gunS)
		if usingArc || laserBusy {
			ey, ep = math.Abs(turn(gunS.Yaw-p.me.Aim[0])), math.Abs(gunS.Pitch-p.me.Aim[1])
		}
		if permit && m.Sight && gunS.Open && g.Ammo > 0 && g.Reload <= 0 && gunS.Dist < reach &&
			ey < math.Atan2(2.1*m.Radius, gunS.Dist) && ep < math.Atan2(1.3*m.MidZ, gunS.Dist) {
			fire[p.gunSlot] = true
		}
	}
	if alt != nil {
		switch alt.Kind {
		case "laser":
			st := &alt.Stats
			inReach := rayS.Dist < st.Range-4
			if alt.Charge <= 0 {
				if permit && alt.Cooldown <= 0.12 && inReach && (m.Sight || m.Recent) {
					fire[p.altSlot] = true
				}
			} else {
				ey, ep := p.torsoErr(&rayS)
				ripe := alt.Charge >= st.Charge-0.05 ||
					(alt.Charge >= st.MinCharge && p.beamDamage(alt.Charge, st)*p.bonusOn(m) >= m.HP*1.03)
				onLine := m.Sight && rayS.Open && inReach &&
					ey < math.Atan2(1.9*m.Radius, rayS.Dist) && ep < math.Atan2(1.3*m.MidZ, rayS.Dist)
				// keep holding until the shot is both ripe and true; letting go fires
				fire[p.altSlot] = !(ripe && onLine)
			}
		case "lightning":
			st := &alt.Stats
			rel := math.Hypot(m.Vel[0]-p.me.Vel[0], m.Vel[1]-p.me.Vel[1])
			ey, _ := p.torsoErr(&rayS)
			if laserBusy || usingArc {
				ey = math.Abs(turn(rayS.Yaw - p.me.Aim[0]))
			}
			if permit && m.Sight && rayS.Open && p.me.Energy >= st.Energy && alt.Cooldown <= 0.06 &&
				rayS.Dist < st.Range-1-0.05*rel && ey < 0.4*st.ConeDeg*math.Pi/180 {
				fire[p.altSlot] = true
			}
		case "mortar":
			if usingArc && alt.Cooldown <= 0.05 {
				ey, ep := math.Abs(turn(arcS.Yaw-p.me.Aim[0])), math.Abs(arcS.Pitch-p.me.Aim[1])
				if ey < 0.03 && ep < 0.02 {
					fire[p.altSlot] = true
				}
			}
		}
	}
	o.Fire0, o.Fire1 = fire[0], fire[1]
}

// bonusOn: damage multiplier a laser gets against this mark (mortar cook-off).
func (p *Pilot) bonusOn(m *Mark) float64 {
	if m.Mortar {
		return 1 + p.R.Weapons.Laser.Cookoff
	}
	return 1
}

// holdCharge keeps a laser that is already charging from firing into nothing.
func (p *Pilot) holdCharge(o *Order) {
	if p.alt != nil && p.alt.Kind == "laser" && p.alt.Charge > 0 {
		full := p.alt.Stats.Charge + p.alt.Stats.HoldS
		if p.alt.Charge < full-0.4 {
			if p.altSlot == 0 {
				o.Fire0 = true
			} else {
				o.Fire1 = true
			}
		}
	}
}
