package main

// Wire formats for the mechfight/1 line protocol. Only the fields the pilot reads are declared;
// anything else on the wire is ignored by the decoder.

import (
	"math"
	"strconv"
)

type Rules struct {
	Match struct {
		TickHz      float64 `json:"tick_hz"`
		DecideEvery float64 `json:"decide_every"`
		Duration    float64 `json:"duration_s"`
	} `json:"match"`
	Mech struct {
		Radius      float64 `json:"radius"`
		Height      float64 `json:"height"`
		EyeHeight   float64 `json:"eye_height"`
		GroundAccel float64 `json:"ground_accel"`
		AirAccel    float64 `json:"air_accel"`
		Gravity     float64 `json:"gravity"`
		JumpSpeed   float64 `json:"jump_speed"`
		JetAccel    float64 `json:"jet_accel"`
		MaxRise     float64 `json:"max_rise_speed"`
		StepUp      float64 `json:"step_up"`
		MaxSlopeDeg float64 `json:"max_slope_deg"`
		RegenDelay  float64 `json:"regen_delay"`
		RearMult    float64 `json:"rear_mult"`
		FrontArcDeg float64 `json:"front_arc_deg"`
	} `json:"mech"`
	Vision struct {
		Range   float64 `json:"range"`
		FovDeg  float64 `json:"fov_deg"`
		Near    float64 `json:"near_range"`
		MemoryS float64 `json:"memory_s"`
	} `json:"vision"`
	Weapons struct {
		Gun struct {
			Damage, Cooldown, Speed, Range float64
			Mag, Reload                    float64
		} `json:"gun"`
		Mortar struct {
			Damage, Splash  float64
			SplashRadius    float64 `json:"splash_radius"`
			Cooldown, Speed float64
			Gravity, TTL    float64
		} `json:"mortar"`
		Laser struct {
			Damage, Charge float64
			MinCharge      float64 `json:"min_charge"`
			MinFrac        float64 `json:"min_frac"`
			HoldS          float64 `json:"hold_s"`
			Cooldown       float64
			Range          float64
			Cookoff        float64
		} `json:"laser"`
		Lightning struct {
			Damage, Cooldown, Range float64
			ConeDeg                 float64 `json:"cone_deg"`
			Energy                  float64
			ShortS                  float64 `json:"short_s"`
		} `json:"lightning"`
	} `json:"weapons"`
	Cubes struct {
		PickupRadius float64 `json:"pickup_radius"`
		StatCap      float64 `json:"stat_cap"`
		DmgPerCube   float64 `json:"damage_per_cube"`
	} `json:"cubes"`
	Loot struct {
		PickupRadius float64 `json:"pickup_radius"`
	} `json:"loot"`
	Npc struct {
		Aggro  float64 `json:"aggro_range"`
		Speed  float64 `json:"speed"`
		Damage float64 `json:"damage"`
	} `json:"npc"`
	Drops struct {
		Radius   float64 `json:"radius"`
		CaptureS float64 `json:"capture_s"`
	} `json:"drops"`
	Upgrades []struct {
		ID       string `json:"id"`
		Requires string `json:"requires"`
		Max      int    `json:"max"`
	} `json:"upgrades"`
}

type Greeting struct {
	Type string  `json:"type"`
	Slot int     `json:"slot"`
	Seed float64 `json:"seed"`
	Map  struct {
		Dir string `json:"dir"`
	} `json:"map"`
	Config Rules `json:"config"`
}

type ArmStats struct {
	Damage       float64 `json:"damage"`
	Cooldown     float64 `json:"cooldown"`
	Speed        float64 `json:"speed"`
	Range        float64 `json:"range"`
	SpreadDeg    float64 `json:"spread_deg"`
	Mag          float64 `json:"mag"`
	Reload       float64 `json:"reload"`
	Charge       float64 `json:"charge"`
	MinCharge    float64 `json:"min_charge"`
	MinFrac      float64 `json:"min_frac"`
	HoldS        float64 `json:"hold_s"`
	ConeDeg      float64 `json:"cone_deg"`
	Energy       float64 `json:"energy"`
	Gravity      float64 `json:"gravity"`
	Splash       float64 `json:"splash"`
	SplashRadius float64 `json:"splash_radius"`
	Cookoff      float64 `json:"cookoff"`
}

type Arm struct {
	Kind     string   `json:"kind"`
	Cooldown float64  `json:"cooldown"`
	Ammo     float64  `json:"ammo"`
	Reload   float64  `json:"reload"`
	Charge   float64  `json:"charge"`
	Stats    ArmStats `json:"stats"`
}

type HullStats struct {
	Radius       float64 `json:"radius"`
	Height       float64 `json:"height"`
	EyeHeight    float64 `json:"eye_height"`
	MuzzleHeight float64 `json:"muzzle_height"`
	WalkSpeed    float64 `json:"walk_speed"`
	BoostSpeed   float64 `json:"boost_speed"`
	GroundAccel  float64 `json:"ground_accel"`
	TurnRate     float64 `json:"turn_rate"`
	PitchRate    float64 `json:"pitch_rate"`
	MinPitch     float64 `json:"min_pitch"`
	MaxPitch     float64 `json:"max_pitch"`
	BoostDrain   float64 `json:"boost_drain"`
	JetDrain     float64 `json:"jet_drain"`
	EnergyRegen  float64 `json:"energy_regen"`
	RegenDelay   float64 `json:"regen_delay"`
	RegenPerS    float64 `json:"regen_per_s"`
	ChargeMove   float64 `json:"charge_move_mult"`
	Armor        float64 `json:"armor"`
	NearBonus    float64 `json:"near_range_bonus"`
	VisionBonus  float64 `json:"vision_bonus"`
}

type Own struct {
	ID        int        `json:"id"`
	Pos       [3]float64 `json:"pos"`
	Vel       [3]float64 `json:"vel"`
	Aim       [2]float64 `json:"aim"`
	HP        float64    `json:"hp"`
	MaxHP     float64    `json:"max_hp"`
	Energy    float64    `json:"energy"`
	MaxEnergy float64    `json:"max_energy"`
	Grounded  bool       `json:"grounded"`
	Cubes     int        `json:"cubes"`
	Upgrades  []string   `json:"upgrades"`
	InZone    bool       `json:"in_zone"`
	Weapons   []*Arm     `json:"weapons"`
	Stats     HullStats  `json:"stats"`
}

type Actor struct {
	ID       int        `json:"id"`
	Kind     string     `json:"kind"`
	Pos      [3]float64 `json:"pos"`
	Vel      [3]float64 `json:"vel"`
	Aim      [2]float64 `json:"aim"`
	HP       *float64   `json:"hp"`
	MaxHP    float64    `json:"max_hp"`
	Cubes    int        `json:"cubes"`
	Weapons  []string   `json:"weapons"`
	Charging bool       `json:"charging"`
}

type Round struct {
	ID   int        `json:"id"`
	Kind string     `json:"kind"`
	Pos  [3]float64 `json:"pos"`
	Vel  [3]float64 `json:"vel"`
}

type Thing struct {
	ID   int        `json:"id"`
	Kind string     `json:"kind"`
	Pos  [3]float64 `json:"pos"`
	HP   float64    `json:"hp"`
}

type Noise struct {
	Kind    string  `json:"kind"`
	Bearing float64 `json:"bearing"`
	Dist    string  `json:"dist"`
	Age     float64 `json:"age"`
}

type Beam struct {
	By   int          `json:"by"`
	Kind string       `json:"kind"`
	Path [][3]float64 `json:"path"`
}

type Happening struct {
	Type    string   `json:"type"`
	Amount  float64  `json:"amount"`
	Weapon  string   `json:"weapon"`
	Bearing *float64 `json:"bearing"`
	By      *int     `json:"by"`
	Rear    bool     `json:"rear"`
	Target  int      `json:"target"`
	Effect  string   `json:"effect"`
	Reason  string   `json:"reason"`
}

type Crate struct {
	ID       int        `json:"id"`
	Pos      [3]float64 `json:"pos"`
	Radius   float64    `json:"radius"`
	LandsIn  float64    `json:"lands_in"`
	CaptureS float64    `json:"capture_s"`
	Progress float64    `json:"progress"`
	Holder   *int       `json:"holder"`
}

type Ring struct {
	Center      [2]float64 `json:"center"`
	Radius      float64    `json:"radius"`
	NextCenter  [2]float64 `json:"next_center"`
	NextRadius  float64    `json:"next_radius"`
	ShrinkStart float64    `json:"shrink_start"`
	ShrinkEnd   float64    `json:"shrink_end"`
	DPS         float64    `json:"dps"`
}

type Offer struct {
	ID      float64 `json:"id"`
	Choices []struct {
		ID string `json:"id"`
	} `json:"choices"`
}

type Frame struct {
	Type    string      `json:"type"`
	Tick    float64     `json:"tick"`
	T       float64     `json:"t"`
	Alive   int         `json:"alive"`
	Self    Own         `json:"self"`
	Mechs   []Actor     `json:"mechs"`
	Shots   []Round     `json:"projectiles"`
	Crates  []Thing     `json:"crates"`
	Cubes   []Thing     `json:"cubes"`
	Pickups []Thing     `json:"pickups"`
	Sounds  []Noise     `json:"sounds"`
	Beams   []Beam      `json:"beams"`
	Events  []Happening `json:"events"`
	Drops   []Crate     `json:"drops"`
	Zone    Ring        `json:"zone"`
	Offer   *Offer      `json:"offer"`
}

// Order is one decision sent back to the server.
type Order struct {
	MoveX, MoveY float64
	Boost, Jump  bool
	Yaw, Pitch   float64
	Fire0, Fire1 bool
	Reload       bool
	Pick         int // -1 = none
	Take         int // -1 = none
	Drop         int // -1 = none
	Note         string
}

func sane(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func putNum(b []byte, v float64) []byte {
	return strconv.AppendFloat(b, math.Round(sane(v)*10000)/10000, 'f', -1, 64)
}

func putBool(b []byte, v bool) []byte {
	if v {
		return append(b, "true"...)
	}
	return append(b, "false"...)
}

// Encode renders the order as one JSON line (with trailing newline) for the given tick.
func (o *Order) Encode(b []byte, tick int64) []byte {
	mx, my := sane(o.MoveX), sane(o.MoveY)
	if n := math.Hypot(mx, my); n > 1 {
		mx, my = mx/n, my/n
	}
	b = append(b, `{"tick":`...)
	b = strconv.AppendInt(b, tick, 10)
	b = append(b, `,"move":[`...)
	b = putNum(b, mx)
	b = append(b, ',')
	b = putNum(b, my)
	b = append(b, `],"boost":`...)
	b = putBool(b, o.Boost)
	b = append(b, `,"jump":`...)
	b = putBool(b, o.Jump)
	b = append(b, `,"aim":[`...)
	b = putNum(b, o.Yaw)
	b = append(b, ',')
	b = putNum(b, o.Pitch)
	b = append(b, `],"fire":[`...)
	b = putBool(b, o.Fire0)
	b = append(b, ',')
	b = putBool(b, o.Fire1)
	b = append(b, `],"reload":`...)
	b = putBool(b, o.Reload)
	if o.Pick >= 0 {
		b = append(b, `,"pick":`...)
		b = strconv.AppendInt(b, int64(o.Pick), 10)
	}
	if o.Take >= 0 {
		b = append(b, `,"take":`...)
		b = strconv.AppendInt(b, int64(o.Take), 10)
	}
	if o.Drop >= 0 {
		b = append(b, `,"drop":`...)
		b = strconv.AppendInt(b, int64(o.Drop), 10)
	}
	b = append(b, "}\n"...)
	return b
}
