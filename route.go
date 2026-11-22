package main

// Walking graph on a two-metre lattice laid over the arena columns, plus an A* router with
// reusable scratch buffers and a few helpers (straight-walk test, snapping, region labels).

import (
	"container/heap"
	"math"
)

const pitchM = 2 // lattice spacing in metres (and in columns)

type Lattice struct {
	A      *Arena
	W, H   int
	Open   []bool    // a mech can stand here
	Lvl    []float32 // standing height
	Tight  []bool    // open but brushing an obstacle
	Region []int32   // walk-connected label (0 = none)
	slope  float64   // max rise per metre

	// search scratch
	gen    uint32
	mark   []uint32
	closed []uint32
	cost   []float32
	from   []int32
	pq     frontier
}

type frontierItem struct {
	f    float32
	node int32
}
type frontier []frontierItem

func (q frontier) Len() int            { return len(q) }
func (q frontier) Less(i, j int) bool  { return q[i].f < q[j].f }
func (q frontier) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *frontier) Push(x interface{}) { *q = append(*q, x.(frontierItem)) }
func (q *frontier) Pop() interface{} {
	o := *q
	n := len(o)
	it := o[n-1]
	*q = o[:n-1]
	return it
}

var hood = [8][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}}

func BuildLattice(a *Arena, bodyR, bodyH, stepUp, slopeDeg float64) *Lattice {
	l := &Lattice{A: a, W: (a.NX-1)/pitchM + 1, H: (a.NY-1)/pitchM + 1}
	n := l.W * l.H
	l.Open = make([]bool, n)
	l.Tight = make([]bool, n)
	l.Lvl = make([]float32, n)
	l.Region = make([]int32, n)
	l.mark = make([]uint32, n)
	l.closed = make([]uint32, n)
	l.cost = make([]float32, n)
	l.from = make([]int32, n)
	l.slope = math.Tan(slopeDeg * math.Pi / 180)
	if l.slope <= 0 || l.slope > 3 {
		l.slope = 1.19
	}
	reach := int(math.Ceil(bodyR + 0.5))
	wide := reach + 2
	for J := 0; J < l.H; J++ {
		for I := 0; I < l.W; I++ {
			ci, cj := I*pitchM, J*pitchM
			k := J*l.W + I
			c := cj*a.NX + ci
			h := float64(a.Deck[c])
			l.Lvl[k] = float32(h)
			x, y := a.OX+float64(ci), a.OY+float64(cj)
			if !a.Inside(x, y, bodyR+1) {
				continue
			}
			ok, tight := true, false
			for dj := -wide; dj <= wide && ok; dj++ {
				for di := -wide; di <= wide; di++ {
					ii, jj := ci+di, cj+dj
					if ii < 0 || jj < 0 || ii >= a.NX || jj >= a.NY {
						continue
					}
					d := math.Hypot(float64(di), float64(dj))
					if d > float64(wide) {
						continue
					}
					cc := jj*a.NX + ii
					rise := float64(a.Deck[cc]) - h
					wall := rise > stepUp+l.slope*d*0.75
					roofed := float64(a.SlabLo[cc]) < h+bodyH+0.3 && float64(a.SlabHi[cc]) > h+stepUp
					if wall || roofed {
						if d <= bodyR+0.6 {
							ok = false
							break
						}
						tight = true
					}
				}
			}
			l.Open[k] = ok
			l.Tight[k] = ok && tight
		}
	}
	l.label()
	return l
}

// passable: can a mech walk from lattice node a to its neighbour b?
func (l *Lattice) passable(a, b int, dist float64) bool {
	if !l.Open[b] {
		return false
	}
	rise := float64(l.Lvl[b] - l.Lvl[a])
	return rise <= l.slope*dist+0.25
}

func (l *Lattice) label() {
	var next int32 = 1
	stack := make([]int32, 0, 4096)
	for s := range l.Open {
		if !l.Open[s] || l.Region[s] != 0 {
			continue
		}
		l.Region[s] = next
		stack = append(stack[:0], int32(s))
		for len(stack) > 0 {
			k := int(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			I, J := k%l.W, k/l.W
			for _, o := range hood[:4] {
				ni, nj := I+o[0], J+o[1]
				if ni < 0 || nj < 0 || ni >= l.W || nj >= l.H {
					continue
				}
				m := nj*l.W + ni
				// regions are two-way walkable: a drop off a roof does not merge them
				if l.Region[m] == 0 && l.passable(k, m, pitchM) && l.passable(m, k, pitchM) {
					l.Region[m] = next
					stack = append(stack, int32(m))
				}
			}
		}
		next++
	}
}

func (l *Lattice) XY(k int) (float64, float64) {
	return l.A.OX + float64((k%l.W)*pitchM), l.A.OY + float64((k/l.W)*pitchM)
}

func (l *Lattice) Near(x, y float64) int {
	I := int(math.Floor((x-l.A.OX)/pitchM + 0.5))
	J := int(math.Floor((y-l.A.OY)/pitchM + 0.5))
	if I < 0 {
		I = 0
	} else if I >= l.W {
		I = l.W - 1
	}
	if J < 0 {
		J = 0
	} else if J >= l.H {
		J = l.H - 1
	}
	return J*l.W + I
}

// Snap finds the closest standable node to a point, preferring nodes whose height is close to z
// (pass NaN to ignore height). Returns -1 when nothing is within span nodes.
func (l *Lattice) Snap(x, y, z float64, span int) int {
	k0 := l.Near(x, y)
	I0, J0 := k0%l.W, k0/l.W
	best, bestD := -1, math.Inf(1)
	for dj := -span; dj <= span; dj++ {
		for di := -span; di <= span; di++ {
			I, J := I0+di, J0+dj
			if I < 0 || J < 0 || I >= l.W || J >= l.H {
				continue
			}
			k := J*l.W + I
			if !l.Open[k] {
				continue
			}
			nx, ny := l.XY(k)
			d := math.Hypot(nx-x, ny-y)
			if !math.IsNaN(z) {
				d += 3 * math.Abs(float64(l.Lvl[k])-z)
			}
			if d < bestD {
				best, bestD = k, d
			}
		}
	}
	return best
}

// Standable reports whether a mech centred at the point is on open ground at roughly height z.
func (l *Lattice) Standable(x, y, z float64) bool {
	k := l.Near(x, y)
	return l.Open[k] && (math.IsNaN(z) || math.Abs(float64(l.Lvl[k])-z) < 3.5)
}

// Walkable tests a straight stroll between two points using the lattice.
func (l *Lattice) Walkable(ax, ay, bx, by float64) bool {
	d := math.Hypot(bx-ax, by-ay)
	steps := int(d/1.5) + 1
	prev := l.Near(ax, ay)
	if !l.Open[prev] {
		return false
	}
	for s := 1; s <= steps; s++ {
		f := float64(s) / float64(steps)
		k := l.Near(ax+(bx-ax)*f, ay+(by-ay)*f)
		if k == prev {
			continue
		}
		if !l.passable(prev, k, pitchM*1.2) {
			return false
		}
		prev = k
	}
	return true
}

// Route runs A* from start to goal (node indices). toll adds a per-node surcharge (may be nil).
// budget caps node expansions; when the goal is not reached the path to the node that got
// closest is returned with done=false.
func (l *Lattice) Route(start, goal, budget int, toll func(k int) float32) (path []int32, done bool) {
	if start < 0 || goal < 0 {
		return nil, false
	}
	l.gen++
	if l.gen == 0 {
		for i := range l.mark {
			l.mark[i], l.closed[i] = 0, 0
		}
		l.gen = 1
	}
	gx, gy := goal%l.W, goal/l.W
	est := func(k int) float32 {
		dx := math.Abs(float64(k%l.W - gx))
		dy := math.Abs(float64(k/l.W - gy))
		lo, hi := math.Min(dx, dy), math.Max(dx, dy)
		return float32((hi + 0.4142*lo) * pitchM)
	}
	l.pq = l.pq[:0]
	l.mark[start] = l.gen
	l.cost[start] = 0
	l.from[start] = -1
	heap.Push(&l.pq, frontierItem{est(start), int32(start)})
	closest, closestH := start, est(start)
	for l.pq.Len() > 0 && budget > 0 {
		it := heap.Pop(&l.pq).(frontierItem)
		k := int(it.node)
		if l.closed[k] == l.gen {
			continue
		}
		l.closed[k] = l.gen
		budget--
		if k == goal {
			closest = k
			done = true
			break
		}
		if h := est(k); h < closestH {
			closest, closestH = k, h
		}
		I, J := k%l.W, k/l.W
		for n, o := range hood {
			ni, nj := I+o[0], J+o[1]
			if ni < 0 || nj < 0 || ni >= l.W || nj >= l.H {
				continue
			}
			m := nj*l.W + ni
			if l.closed[m] == l.gen {
				continue
			}
			step := float64(pitchM)
			if n >= 4 {
				step *= math.Sqrt2
				// no cutting corners
				if !l.Open[J*l.W+ni] || !l.Open[nj*l.W+I] {
					continue
				}
			}
			if !l.passable(k, m, step) {
				continue
			}
			c := float32(step)
			if l.Tight[m] {
				c *= 1.5
			}
			if drop := l.Lvl[k] - l.Lvl[m]; drop > 3 {
				c += 4
			}
			if toll != nil {
				c += toll(m)
			}
			g := l.cost[k] + c
			if l.mark[m] != l.gen || g < l.cost[m] {
				l.mark[m] = l.gen
				l.cost[m] = g
				l.from[m] = int32(k)
				heap.Push(&l.pq, frontierItem{g + est(m), int32(m)})
			}
		}
	}
	for k := int32(closest); k >= 0; k = l.from[k] {
		path = append(path, k)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, done
}

// Spill visits nodes outward from start in order of walking cost, calling visit for each one
// until it returns true or the cost/limit is exhausted. Returns the accepted node or -1.
func (l *Lattice) Spill(start int, maxCost float32, limit int, visit func(k int, cost float32) bool) int {
	if start < 0 {
		return -1
	}
	l.gen++
	l.pq = l.pq[:0]
	l.mark[start] = l.gen
	l.cost[start] = 0
	heap.Push(&l.pq, frontierItem{0, int32(start)})
	for l.pq.Len() > 0 && limit > 0 {
		it := heap.Pop(&l.pq).(frontierItem)
		k := int(it.node)
		if l.closed[k] == l.gen {
			continue
		}
		l.closed[k] = l.gen
		limit--
		if visit(k, it.f) {
			return k
		}
		I, J := k%l.W, k/l.W
		for _, o := range hood[:4] {
			ni, nj := I+o[0], J+o[1]
			if ni < 0 || nj < 0 || ni >= l.W || nj >= l.H {
				continue
			}
			m := nj*l.W + ni
			if l.closed[m] == l.gen || !l.passable(k, m, pitchM) {
				continue
			}
			g := it.f + pitchM
			if g > maxCost {
				continue
			}
			if l.mark[m] != l.gen || g < l.cost[m] {
				l.mark[m] = l.gen
				l.cost[m] = g
				heap.Push(&l.pq, frontierItem{g, int32(m)})
			}
		}
	}
	return -1
}
