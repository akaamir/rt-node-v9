package main

// Static map model. The campus is flattened into one-metre columns: every column knows the bare
// terrain height, the height of the surface a mech would stand on (terrain or the roof of a solid
// that rises from the ground), and at most one floating slab above it (bridges, eaves). Rays are
// marched through the columns, which is plenty accurate for cover and line-of-fire questions.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
)

type Arena struct {
	OX, OY float64 // world position of column (0,0)
	NX, NY int
	Dirt   []float32 // terrain height
	Deck   []float32 // standing surface
	SlabLo []float32 // floating slab underside (+Inf when absent)
	SlabHi []float32 // floating slab top
	// playable rectangle
	MinX, MinY, MaxX, MaxY float64
	Peak                   float64 // tallest deck anywhere
}

type solidJSON struct {
	Kind  string      `json:"kind"`
	Base  float64     `json:"base"`
	Top   float64     `json:"top"`
	Ring  []float64   `json:"ring"`
	Holes [][]float64 `json:"holes"`
}

type mapJSON struct {
	Bounds struct {
		MinX, MaxX, MinY, MaxY float64
	} `json:"bounds"`
	Terrain struct {
		MinX  float64 `json:"minX"`
		MinY  float64 `json:"minY"`
		Step  float64 `json:"step"`
		NX    int     `json:"nx"`
		NY    int     `json:"ny"`
		Scale float64 `json:"scale"`
	} `json:"terrain"`
	Prisms []solidJSON `json:"prisms"`
}

func LoadArena(dir string) (*Arena, error) {
	blob, err := os.ReadFile(filepath.Join(dir, "v2", "world.json"))
	if err != nil {
		return nil, err
	}
	var mj mapJSON
	if err := json.Unmarshal(blob, &mj); err != nil {
		return nil, err
	}
	hts, err := os.ReadFile(filepath.Join(dir, "v2", "terrain.i16"))
	if err != nil {
		return nil, err
	}
	nx, ny := mj.Terrain.NX, mj.Terrain.NY
	if nx < 2 || ny < 2 || len(hts) < nx*ny*2 {
		return nil, errors.New("terrain grid malformed")
	}
	if mj.Terrain.Step != 0 && math.Abs(mj.Terrain.Step-1) > 1e-6 {
		return nil, errors.New("unsupported terrain step")
	}
	scale := mj.Terrain.Scale
	if scale == 0 {
		scale = 0.01
	}
	a := &Arena{OX: mj.Terrain.MinX, OY: mj.Terrain.MinY, NX: nx, NY: ny,
		MinX: mj.Bounds.MinX, MinY: mj.Bounds.MinY, MaxX: mj.Bounds.MaxX, MaxY: mj.Bounds.MaxY}
	n := nx * ny
	a.Dirt = make([]float32, n)
	a.Deck = make([]float32, n)
	a.SlabLo = make([]float32, n)
	a.SlabHi = make([]float32, n)
	inf := float32(math.Inf(1))
	for i := 0; i < n; i++ {
		v := float32(float64(int16(binary.LittleEndian.Uint16(hts[2*i:]))) * scale)
		a.Dirt[i] = v
		a.Deck[i] = v
		a.SlabLo[i] = inf
		a.SlabHi[i] = inf
	}
	solids := mj.Prisms
	sort.SliceStable(solids, func(i, j int) bool { return solids[i].Base < solids[j].Base })
	for k := range solids {
		a.stamp(&solids[k])
	}
	for i := 0; i < n; i++ {
		if float64(a.Deck[i]) > a.Peak {
			a.Peak = float64(a.Deck[i])
		}
	}
	return a, nil
}

// stamp rasterises one extruded polygon into the column arrays (even-odd fill, holes included).
func (a *Arena) stamp(s *solidJSON) {
	if len(s.Ring) < 6 || s.Top <= s.Base {
		return
	}
	type seg struct{ x0, y0, x1, y1 float64 }
	var segs []seg
	loop := func(r []float64) {
		m := len(r) / 2
		for i := 0; i < m; i++ {
			j := (i + 1) % m
			segs = append(segs, seg{r[2*i], r[2*i+1], r[2*j], r[2*j+1]})
		}
	}
	loop(s.Ring)
	for _, h := range s.Holes {
		if len(h) >= 6 {
			loop(h)
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, e := range segs {
		lo = math.Min(lo, math.Min(e.y0, e.y1))
		hi = math.Max(hi, math.Max(e.y0, e.y1))
	}
	j0 := int(math.Ceil(lo - a.OY))
	j1 := int(math.Floor(hi - a.OY))
	if j0 < 0 {
		j0 = 0
	}
	if j1 > a.NY-1 {
		j1 = a.NY - 1
	}
	var xs []float64
	for j := j0; j <= j1; j++ {
		y := a.OY + float64(j) + 1e-4
		xs = xs[:0]
		for _, e := range segs {
			if (e.y0 > y) != (e.y1 > y) {
				xs = append(xs, e.x0+(y-e.y0)*(e.x1-e.x0)/(e.y1-e.y0))
			}
		}
		sort.Float64s(xs)
		for k := 0; k+1 < len(xs); k += 2 {
			i0 := int(math.Ceil(xs[k] - a.OX))
			i1 := int(math.Floor(xs[k+1] - a.OX))
			if i0 < 0 {
				i0 = 0
			}
			if i1 > a.NX-1 {
				i1 = a.NX - 1
			}
			for i := i0; i <= i1; i++ {
				c := j*a.NX + i
				base, top := float32(s.Base), float32(s.Top)
				if base <= a.Deck[c]+1.5 {
					if top > a.Deck[c] {
						a.Deck[c] = top
					}
					// a rising deck can swallow a slab recorded earlier
					if a.SlabLo[c] <= a.Deck[c]+1.5 {
						if a.SlabHi[c] > a.Deck[c] {
							a.Deck[c] = a.SlabHi[c]
						}
						a.SlabLo[c], a.SlabHi[c] = float32(math.Inf(1)), float32(math.Inf(1))
					}
				} else if math.IsInf(float64(a.SlabLo[c]), 1) {
					a.SlabLo[c], a.SlabHi[c] = base, top
				} else if base <= a.SlabHi[c]+0.5 && top > a.SlabHi[c] {
					a.SlabHi[c] = top
				}
			}
		}
	}
}

func (a *Arena) col(x, y float64) int {
	i := int(math.Floor(x - a.OX + 0.5))
	j := int(math.Floor(y - a.OY + 0.5))
	if i < 0 {
		i = 0
	} else if i >= a.NX {
		i = a.NX - 1
	}
	if j < 0 {
		j = 0
	} else if j >= a.NY {
		j = a.NY - 1
	}
	return j*a.NX + i
}

// Inside reports whether the point lies in the playable rectangle shrunk by pad.
func (a *Arena) Inside(x, y, pad float64) bool {
	return x >= a.MinX+pad && x <= a.MaxX-pad && y >= a.MinY+pad && y <= a.MaxY-pad
}

// DeckAt is the standing surface at a point.
func (a *Arena) DeckAt(x, y float64) float64 { return float64(a.Deck[a.col(x, y)]) }

// DirtAt is the smooth terrain height (bilinear).
func (a *Arena) DirtAt(x, y float64) float64 {
	fx := math.Min(math.Max(x-a.OX, 0), float64(a.NX-1)-1e-6)
	fy := math.Min(math.Max(y-a.OY, 0), float64(a.NY-1)-1e-6)
	i, j := int(fx), int(fy)
	tx, ty := fx-float64(i), fy-float64(j)
	k := j*a.NX + i
	h00, h10 := float64(a.Dirt[k]), float64(a.Dirt[k+1])
	h01, h11 := float64(a.Dirt[k+a.NX]), float64(a.Dirt[k+a.NX+1])
	return (h00*(1-tx)+h10*tx)*(1-ty) + (h01*(1-tx)+h11*tx)*ty
}

// FootAt returns the surface a mech with feet near z would rest on at (x,y): the roof when the
// feet are at roof level, the smooth terrain otherwise.
func (a *Arena) FootAt(x, y float64) float64 {
	c := a.col(x, y)
	if a.Deck[c] > a.Dirt[c]+0.05 {
		return float64(a.Deck[c])
	}
	return a.DirtAt(x, y)
}

func (a *Arena) solid(c int, z float64) bool {
	if z < float64(a.Deck[c]) {
		return true
	}
	return z > float64(a.SlabLo[c]) && z < float64(a.SlabHi[c])
}

// Reach marches from p along the unit direction d and returns how far the ray travels before it
// enters something solid (maxT when it never does).
func (a *Arena) Reach(px, py, pz, dx, dy, dz, maxT float64) float64 {
	if maxT <= 0 || math.IsNaN(px+py+pz+dx+dy+dz+maxT) {
		return 0
	}
	const stride = 0.8
	// leave the starting column untested so a shooter hugging a wall is not self-blocked
	for t := stride; t < maxT; t += stride {
		x, y, z := px+dx*t, py+dy*t, pz+dz*t
		if z > a.Peak+1 && dz >= 0 {
			return maxT
		}
		if !a.Inside(x, y, -2) {
			return t
		}
		if a.solid(a.col(x, y), z) {
			return t - stride
		}
	}
	return maxT
}

// Clear reports whether the straight segment between two points is unobstructed. The last
// stretch near the far end is forgiven so targets standing against walls still count.
func (a *Arena) Clear(ax, ay, az, bx, by, bz float64) bool {
	dx, dy, dz := bx-ax, by-ay, bz-az
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if d < 2.4 {
		return true
	}
	return a.Reach(ax, ay, az, dx/d, dy/d, dz/d, d-1.2) >= d-1.2
}
