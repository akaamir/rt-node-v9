package main

// Pilot state and perception: everything the bot remembers between observations, and the code
// that folds a new observation into that memory.

import (
	"math"
	"math/rand"
)

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func turn(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a < -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

func gap2(ax, ay, bx, by float64) float64 { return math.Hypot(ax-bx, ay-by) }

// Contact is a remembered mech or drone.
type Contact struct {
	ID        int
	Drone     bool
	Pos, Vel  [3]float64
	Yaw       float64
	HP, Max   float64
	Cubes     int
	Laser     bool
	Mortar    bool
	Bolt      bool
	Charging  bool
	ChargeT0  float64 // when its current charge was first noticed
	Listed    bool    // present in this observation
	wasListed bool
	Sight     bool    // clear line from our eye right now
	SightT    float64 // last time Sight held
	ViewT     float64 // last time it was genuinely in our sensor cone
	FirstT    float64
	AimT      float64 // last time its torso pointed at us
	StruckT   float64 // last time it damaged us
	Hurt      float64 // decayed damage it has done to us
	TagT      float64 // last time we damaged it
	OtherT    float64 // last time somebody else visibly damaged it
	Ghost     bool    // invented from a damage bearing, never actually seen
	lastHP    float64
	hist      []trail    // recent positions, for scoring lead models
	vgHit     [4]float64 // running hit rate of each lead model against this mech
	vgN       float64
	lastHPT   float64

	// per-tick scratch
	D    float64 // horizontal distance
	Adv  float64 // exchange advantage (ours over theirs)
	Hot  bool    // actively threatening us
	hotT float64
}

func (c *Contact) scale(r *Rules) float64 { return 1 + 0.05*float64(c.Cubes) }

// Stash is a remembered item: cube, crate or weapon pickup.
type Stash struct {
	ID    int
	Class byte // 'c' cube, 'k' crate, 'w' weapon
	Kind  string
	Pos   [3]float64
	HP    float64
	SeenT float64
}

type Pilot struct {
	R   Rules
	A   *Arena
	L   *Lattice
	rng *rand.Rand

	f                           *Frame
	t, dt                       float64
	me                          *Own
	x, y, z                     float64
	hpf                         float64
	bodyR, bodyH, eyeH, muzzleH float64
	walk, sprint                float64
	gun, alt                    *Arm
	gunSlot, altSlot            int

	contacts map[int]*Contact
	stash    map[int]*Stash
	banned   map[int]float64 // item id -> time the ban lifts

	// combat bookkeeping
	hurtT      float64 // last weapon damage taken
	hurtYaw    float64
	hurtBy     int
	droneHurtT float64
	anyHurtT   float64
	pressure   float64 // decayed recent damage (tau 3 s)
	dealtT     float64
	kills      int
	killT      float64
	killSpot   [3]float64

	// lobby reading
	prevAlive  int
	orphans    int // deaths we cannot account for
	crossfire  int // sightings of others fighting each other
	beamFights int // lasers seen landing on a third mech
	crossT     map[int]float64
	melee      bool // free-for-all detected

	// behaviour memory
	fleeing   bool
	fleeT0    float64
	cornerT   float64
	lair      int // lattice node we are hiding at (-1 none)
	lairT     float64
	focus     int // id of the mech we are fighting (-1 none)
	focusT    float64
	lootID    int
	lootT     float64
	zoneGoal  [2]float64
	zoneGoalT float64
	idleSpot  [2]float64
	idleSpotT float64
	roamTo    [2]float64
	roamT     float64
	seenCell  map[int]float64
	sweepT    float64

	// route following
	path     []int32
	pathIdx  int
	pathGoal [2]float64
	pathT    float64
	pathOK   bool

	// steering memory
	lastMove   [2]float64
	strafeSign float64
	strafeFlip float64
	stuckPos   [2]float64
	stuckT     float64
	stuckFor   float64
	shakeT     float64
	shakeDir   [2]float64

	// misc
	soundYaw   float64
	soundT     float64
	takeT      float64
	scanBase   float64
	wantYaw    float64
	lastCmdYaw float64
	vgAll      [4]float64 // lead-model hit rates pooled over every opponent
	vgAllN     float64
	leadPick   float64 // lead fraction for the shot being solved (-1 = default)
}

func NewPilot(g *Greeting) *Pilot {
	p := &Pilot{R: g.Config}
	p.rng = rand.New(rand.NewSource(int64(g.Seed) + int64(g.Slot)*7919 + 17))
	p.contacts = map[int]*Contact{}
	p.stash = map[int]*Stash{}
	p.banned = map[int]float64{}
	p.crossT = map[int]float64{}
	p.seenCell = map[int]float64{}
	p.lair, p.focus, p.lootID = -1, -1, -1
	p.prevAlive = -1
	p.strafeSign = 1
	p.hurtT, p.droneHurtT, p.anyHurtT, p.dealtT, p.killT = -99, -99, -99, -99, -99
	p.soundT, p.fleeT0, p.cornerT, p.pathT, p.lairT = -99, -99, -99, -99, -99
	p.zoneGoalT, p.idleSpotT, p.roamT, p.takeT, p.focusT = -99, -99, -99, -99, -99
	p.dt = 0.1
	if hz, de := p.R.Match.TickHz, p.R.Match.DecideEvery; hz > 0 && de > 0 {
		p.dt = de / hz
	}
	m := &p.R.Mech
	if m.Radius <= 0 {
		m.Radius = 2.5
	}
	if m.Height <= 0 {
		m.Height = 9
	}
	if m.StepUp <= 0 {
		m.StepUp = 1.2
	}
	if m.MaxSlopeDeg <= 0 {
		m.MaxSlopeDeg = 50
	}
	if m.GroundAccel <= 0 {
		m.GroundAccel = 58.5
	}
	for _, dir := range []string{g.Map.Dir, "/arena", "./arena", "arena"} {
		if dir == "" {
			continue
		}
		if a, err := LoadArena(dir); err == nil {
			p.A = a
			break
		}
	}
	if p.A == nil {
		panic("no arena files")
	}
	p.L = BuildLattice(p.A, m.Radius, m.Height, m.StepUp, m.MaxSlopeDeg)
	return p
}

// blindOrder is the emergency brain used when the map could not be loaded.
func blindOrder(f *Frame) Order {
	o := Order{Pick: -1, Take: -1, Drop: -1}
	me := &f.Self
	dx, dy := f.Zone.NextCenter[0]-me.Pos[0], f.Zone.NextCenter[1]-me.Pos[1]
	if d := math.Hypot(dx, dy); d > 12 {
		o.MoveX, o.MoveY = dx/d, dy/d
	}
	o.Boost = !me.InZone
	o.Yaw, o.Pitch = me.Aim[0], 0
	best := math.Inf(1)
	for i := range f.Mechs {
		m := &f.Mechs[i]
		d := gap2(m.Pos[0], m.Pos[1], me.Pos[0], me.Pos[1])
		if m.Kind == "mech" && d < best {
			best = d
			tf := d / 252
			ax, ay := m.Pos[0]+m.Vel[0]*tf-me.Pos[0], m.Pos[1]+m.Vel[1]*tf-me.Pos[1]
			o.Yaw = math.Atan2(ay, ax)
			o.Pitch = math.Atan2(m.Pos[2]+4.5-me.Pos[2]-7, math.Max(d, 1))
			o.Fire0 = d < 200
		}
	}
	if f.Offer != nil {
		o.Pick = 0
	}
	return o
}

func (p *Pilot) cubeGain(n int) float64 {
	c := math.Min(float64(n), math.Max(p.R.Cubes.StatCap, 0))
	per := p.R.Cubes.DmgPerCube
	if per == 0 {
		per = 0.04
	}
	return 1 + per*c
}

func (p *Pilot) armByKind(kind string) (*Arm, int) {
	for i, w := range p.me.Weapons {
		if w != nil && w.Kind == kind {
			return w, i
		}
	}
	return nil, -1
}

// eyeLine: can we (from our eye) see the body centre or top of something standing at pos?
func (p *Pilot) eyeLine(pos [3]float64, mid, top float64) bool {
	ez := p.z + p.eyeH
	return p.A.Clear(p.x, p.y, ez, pos[0], pos[1], pos[2]+mid) || p.A.Clear(p.x, p.y, ez, pos[0], pos[1], pos[2]+top)
}

func (p *Pilot) absorb(f *Frame) {
	p.f = f
	p.t = f.T
	p.me = &f.Self
	me := p.me
	p.x, p.y, p.z = me.Pos[0], me.Pos[1], me.Pos[2]
	p.hpf = me.HP / math.Max(me.MaxHP, 1)
	grow := 1 + 0.05*float64(me.Cubes)
	p.bodyR = math.Max(me.Stats.Radius, p.R.Mech.Radius*grow)
	p.bodyH = math.Max(me.Stats.Height, p.R.Mech.Height*grow)
	p.eyeH = math.Max(me.Stats.EyeHeight, 8*grow)
	p.muzzleH = math.Max(me.Stats.MuzzleHeight, 7*grow)
	p.walk = me.Stats.WalkSpeed
	if p.walk <= 0 {
		p.walk = 11.7
	}
	p.sprint = me.Stats.BoostSpeed
	if p.sprint <= 0 {
		p.sprint = 19.5
	}
	p.gun, p.gunSlot = p.armByKind("gun")
	p.alt, p.altSlot = nil, -1
	for i, w := range me.Weapons {
		if w != nil && w.Kind != "gun" {
			p.alt, p.altSlot = w, i
		}
	}
	p.pressure *= math.Exp(-p.dt / 3)

	// --- contacts
	for _, c := range p.contacts {
		c.wasListed = c.Listed
		c.Listed = false
		c.Sight = false
		c.Hurt *= math.Exp(-p.dt / 4)
	}
	for i := range f.Mechs {
		m := &f.Mechs[i]
		if m.ID == me.ID {
			continue
		}
		c := p.contacts[m.ID]
		if c == nil {
			c = &Contact{ID: m.ID, FirstT: p.t, StruckT: -99, TagT: -99, AimT: -99, OtherT: -99, SightT: -99, ViewT: -99, lastHP: -1}
			p.contacts[m.ID] = c
		}
		c.Ghost = false
		c.Drone = m.Kind == "npc"
		c.Pos, c.Vel, c.Yaw = m.Pos, m.Vel, m.Aim[0]
		c.Cubes = m.Cubes
		c.Max = m.MaxHP
		if c.Max <= 0 {
			c.Max = 1400
			if c.Drone {
				c.Max = 350
			}
		}
		if m.HP != nil {
			c.HP = *m.HP
		} else if c.HP <= 0 {
			c.HP = c.Max
		}
		for _, k := range m.Weapons {
			switch k {
			case "laser":
				c.Laser = true
			case "mortar":
				c.Mortar = true
			case "lightning":
				c.Bolt = true
			}
		}
		if len(m.Weapons) > 0 {
			// the list is authoritative while we see it
			c.Laser, c.Mortar, c.Bolt = false, false, false
			for _, k := range m.Weapons {
				c.Laser = c.Laser || k == "laser"
				c.Mortar = c.Mortar || k == "mortar"
				c.Bolt = c.Bolt || k == "lightning"
			}
		}
		if m.Charging && !c.Charging {
			c.ChargeT0 = p.t
		}
		c.Charging = m.Charging
		c.Listed = true
		c.D = gap2(c.Pos[0], c.Pos[1], p.x, p.y)
		mid, top := 4.5*c.scale(&p.R), 8.6*c.scale(&p.R)
		if c.Drone {
			mid, top = 1.75, 3.3
		}
		c.Sight = p.eyeLine(c.Pos, mid, top)
		if c.Sight {
			c.SightT = p.t
			near := p.R.Vision.Near + me.Stats.NearBonus
			far := p.R.Vision.Range + me.Stats.VisionBonus
			off := math.Abs(turn(math.Atan2(c.Pos[1]-p.y, c.Pos[0]-p.x) - me.Aim[0]))
			if c.D < near-1 || (c.D < far-2 && off < p.R.Vision.FovDeg*math.Pi/360-0.05) {
				c.ViewT = p.t
			}
		}
		if !c.Drone {
			back := math.Atan2(p.y-c.Pos[1], p.x-c.Pos[0])
			if math.Abs(turn(c.Yaw-back)) < 0.12+math.Atan2(1.5*p.bodyR, math.Max(c.D, 1)) {
				c.AimT = p.t
			}
			// somebody else is hurting this mech: a hint that the lobby is not one team
			if c.lastHP > 0 && c.HP < c.lastHP-40 && p.t-c.lastHPT < 0.6 && p.t-c.TagT > 1.2 && c.Sight {
				droneNear := false
				for _, o := range p.contacts {
					if o.Drone && p.t-o.ViewT < 6 && gap2(o.Pos[0], o.Pos[1], c.Pos[0], c.Pos[1]) < 150 {
						droneNear = true
					}
				}
				zx, zy := c.Pos[0]-f.Zone.Center[0], c.Pos[1]-f.Zone.Center[1]
				if !droneNear && math.Hypot(zx, zy) < f.Zone.Radius-3 {
					c.OtherT = p.t
					if p.t-p.crossT[c.ID] > 1 {
						p.crossT[c.ID] = p.t
						p.crossfire++
					}
				}
			}
			c.lastHP, c.lastHPT = c.HP, p.t
			p.scoreLeads(c)
		}
	}
	mem := p.R.Vision.MemoryS
	if mem <= 0 {
		mem = 5
	}
	for id, c := range p.contacts {
		if c.Listed {
			continue
		}
		c.D = gap2(c.Pos[0], c.Pos[1], p.x, p.y)
		// the server keeps listing a tracked mech for memory_s; vanishing early means it died
		if c.wasListed && p.t-c.ViewT < mem-1.5 {
			delete(p.contacts, id)
			continue
		}
		age := p.t - math.Max(c.ViewT, c.StruckT)
		if (c.Ghost && age > 6) || age > 40 {
			delete(p.contacts, id)
		}
	}

	// --- events
	for i := range f.Events {
		e := &f.Events[i]
		switch e.Type {
		case "damaged":
			p.anyHurtT = p.t
			if e.Weapon == "zone" {
				continue
			}
			if e.Weapon == "npc" {
				p.droneHurtT = p.t
				if e.By != nil {
					if c := p.contacts[*e.By]; c != nil {
						c.StruckT = p.t
					}
				}
				continue
			}
			p.hurtT = p.t
			p.pressure += e.Amount
			if e.Bearing != nil {
				p.hurtYaw = *e.Bearing
			}
			p.hurtBy = -1
			if e.By == nil {
				continue
			}
			p.hurtBy = *e.By
			c := p.contacts[*e.By]
			if c == nil || (!c.Listed && p.t-c.ViewT > 2) {
				// an attacker we cannot see: pencil it in along the bearing
				reach := 115.0
				switch e.Weapon {
				case "laser":
					reach = 150
				case "lightning":
					reach = 35
				}
				if c == nil {
					c = &Contact{ID: *e.By, FirstT: p.t, TagT: -99, AimT: -99, OtherT: -99, SightT: -99, ViewT: -99, lastHP: -1, Max: 1400, HP: 1400}
					p.contacts[*e.By] = c
				}
				if e.Bearing != nil {
					gx, gy := p.x+math.Cos(*e.Bearing)*reach, p.y+math.Sin(*e.Bearing)*reach
					c.Pos = [3]float64{gx, gy, p.A.DeckAt(gx, gy)}
					c.Vel = [3]float64{}
					c.Ghost = true
					c.D = reach
				}
			}
			c.Drone = *e.By >= 100
			c.StruckT = p.t
			c.Hurt += e.Amount
			switch e.Weapon {
			case "laser":
				c.Laser = true
			case "mortar":
				c.Mortar = true
			case "lightning":
				c.Bolt = true
			}
		case "hit":
			p.dealtT = p.t
			if c := p.contacts[e.Target]; c != nil {
				c.TagT = p.t
			}
		case "kill":
			p.kills++
			p.killT = p.t
			if c := p.contacts[e.Target]; c != nil {
				p.killSpot = c.Pos
				delete(p.contacts, e.Target)
			}
			if p.focus == e.Target {
				p.focus = -1
			}
			// a kill clears the panic state so we scoop the wreck instead of running from it
			p.fleeing = false
			p.pressure *= 0.4
		case "took":
			p.takeT = p.t
		case "take_failed":
			if e.Reason == "gone" || e.Reason == "have" {
				p.banned[p.lootID] = p.t + 30
			}
		}
	}

	// --- beams: lasers and arcs exchanged between strangers
	for i := range f.Beams {
		b := &f.Beams[i]
		if b.By == me.ID || b.By >= 100 || len(b.Path) < 2 {
			continue
		}
		end := b.Path[len(b.Path)-1]
		if gap2(end[0], end[1], p.x, p.y) < 9 {
			continue
		}
		for _, c := range p.contacts {
			if c.Drone || c.ID == b.By || !c.Listed {
				continue
			}
			if gap2(end[0], end[1], c.Pos[0], c.Pos[1]) < 5.5 && p.t-p.crossT[b.By+1000] > 1 {
				p.crossT[b.By+1000] = p.t
				c.OtherT = p.t
				if b.Kind == "laser" {
					p.beamFights++ // a pack never lasers its own
				}
			}
		}
	}

	// --- sounds: remember the freshest nearby mechanical noise for idle scanning
	for i := range f.Sounds {
		s := &f.Sounds[i]
		if s.Dist == "far" {
			continue
		}
		switch s.Kind {
		case "gun", "laser", "lightning", "mortar", "laser_charge", "boost", "step", "jet", "reload":
			p.soundT, p.soundYaw = p.t-s.Age, s.Bearing
		}
	}

	// --- items
	note := func(list []Thing, class byte) {
		for i := range list {
			it := &list[i]
			s := p.stash[it.ID]
			if s == nil {
				s = &Stash{ID: it.ID, Class: class}
				p.stash[it.ID] = s
			}
			s.Kind, s.Pos, s.HP, s.SeenT = it.Kind, it.Pos, it.HP, p.t
		}
	}
	note(f.Cubes, 'c')
	note(f.Crates, 'k')
	note(f.Pickups, 'w')
	near := (p.R.Vision.Near + me.Stats.NearBonus) * 0.85
	for id, s := range p.stash {
		if s.SeenT == p.t {
			continue
		}
		gone := p.t-s.SeenT > 240
		if !gone && gap2(s.Pos[0], s.Pos[1], p.x, p.y) < near && p.A.Clear(p.x, p.y, p.z+p.eyeH, s.Pos[0], s.Pos[1], s.Pos[2]+1) {
			gone = true // we should be seeing it and are not
		}
		if gone {
			delete(p.stash, id)
		}
	}
	for id, until := range p.banned {
		if p.t > until {
			delete(p.banned, id)
		}
	}

	// --- lobby reading: is this one hunting pack or a real free-for-all?
	if p.prevAlive < 0 {
		p.prevAlive = f.Alive
	}
	if fell := p.prevAlive - f.Alive; fell > 0 {
		ours := 0
		for i := range f.Events {
			if f.Events[i].Type == "kill" {
				ours++
			}
		}
		if fell > ours {
			p.orphans += fell - ours
		}
	}
	p.prevAlive = f.Alive
	if !p.melee && knob("melee", 0) >= 0 {
		if (p.orphans >= 3 && p.t < 60) || (p.orphans >= 5 && p.t < 150) || p.beamFights >= 2 || knob("melee", 0) > 0 {
			p.melee = true
		}
	}

	// remember which parts of the map we have looked at (64 m cells)
	p.seenCell[p.cell64(p.x, p.y)] = p.t
}

func (p *Pilot) cell64(x, y float64) int {
	return int(math.Floor(y/64))*1000 + int(math.Floor(x/64))
}

// trail is one remembered sample of a contact's motion.
type trail struct {
	t      float64
	x, y   float64
	vx, vy float64
}

// leadModels are the fractions of a target's velocity a shot may lead by. Which one lands most
// often depends on how that particular opponent moves, so each is scored per contact with
// imaginary shots ("virtual guns") and the best one is used once enough evidence is in.
var leadModels = [4]float64{0, 0.4, 0.75, 1}

func (p *Pilot) scoreLeads(c *Contact) {
	if !c.Sight {
		c.hist = c.hist[:0]
		return
	}
	if n := len(c.hist); n > 0 && p.t-c.hist[n-1].t > 0.25 {
		c.hist = c.hist[:0]
	}
	c.hist = append(c.hist, trail{p.t, c.Pos[0], c.Pos[1], c.Vel[0], c.Vel[1]})
	if len(c.hist) > 40 {
		c.hist = c.hist[len(c.hist)-40:]
	}
	speed := p.R.Weapons.Gun.Speed
	if p.gun != nil && p.gun.Stats.Speed > 0 {
		speed = p.gun.Stats.Speed
	}
	if c.D < 30 || c.D > 235 || speed <= 0 {
		return
	}
	T := lagS + math.Max(c.D-3, 0)/speed
	var old *trail
	for i := len(c.hist) - 1; i >= 0; i-- {
		if math.Abs(c.hist[i].t-(p.t-T)) < 0.06 {
			old = &c.hist[i]
			break
		}
	}
	if old == nil {
		return
	}
	// lateral miss of each model's prediction, measured across our line of fire
	bx, by := c.Pos[0]-p.x, c.Pos[1]-p.y
	bn := math.Hypot(bx, by)
	if bn < 1 {
		return
	}
	nx, ny := -by/bn, bx/bn
	rad := 2.5 * c.scale(&p.R) * 0.9
	for k, frac := range leadModels {
		px, py := old.x+old.vx*T*frac, old.y+old.vy*T*frac
		miss := math.Abs((px-c.Pos[0])*nx + (py-c.Pos[1])*ny)
		hit := 0.0
		if miss < rad {
			hit = 1
		}
		c.vgHit[k] = c.vgHit[k]*0.985 + hit*0.015
		p.vgAll[k] = p.vgAll[k]*0.995 + hit*0.005
	}
	c.vgN++
	p.vgAllN++
}

// bestLead returns the velocity fraction to lead this contact by, or -1 for the default schedule.
func (p *Pilot) bestLead(c *Contact) float64 {
	if knob("vgun", 0) <= 0 {
		return -1
	}
	rates, n := c.vgHit, c.vgN
	if n < knob("vgunN", 25) {
		rates, n = p.vgAll, p.vgAllN
		if n < 60 {
			return -1
		}
	}
	best := 0
	for k := range rates {
		if rates[k] > rates[best]*1.03 {
			best = k
		}
	}
	return leadModels[best]
}
