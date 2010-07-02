package main

import (
	"sdl"
	"gl"
	"unsafe"
	"flag"
	"fmt"
	"runtime"
	"container/list"
)

var iterations = flag.Int("i", 1024, "number of iterations in mandelbrot")
var workers = flag.Int("w", runtime.GOMAXPROCS(0)-1, "number of rendering workers")
var tilesDiv = flag.Int("t", 8, "affects number of tiles, should be power of two")
var noVSync = flag.Bool("no-vsync", false, "disables vsync")

func drawQuad(x, y, w, h int, u, v, u2, v2 float) {
	gl.Begin(gl.QUADS)

	gl.TexCoord2f(gl.GLfloat(u), gl.GLfloat(v))
	gl.Vertex2i(gl.GLint(x), gl.GLint(y))

	gl.TexCoord2f(gl.GLfloat(u2), gl.GLfloat(v))
	gl.Vertex2i(gl.GLint(x+w), gl.GLint(y))

	gl.TexCoord2f(gl.GLfloat(u2), gl.GLfloat(v2))
	gl.Vertex2i(gl.GLint(x+w), gl.GLint(y+h))

	gl.TexCoord2f(gl.GLfloat(u), gl.GLfloat(v2))
	gl.Vertex2i(gl.GLint(x), gl.GLint(y+h))

	gl.End()
}

func uploadTexture_RGBA32(w, h int, data []byte) gl.GLuint {
	var id gl.GLuint

	gl.GenTextures(1, &id)
	gl.BindTexture(gl.TEXTURE_2D, id)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.GLsizei(w), gl.GLsizei(h), 0, gl.RGBA,
		      gl.UNSIGNED_BYTE, unsafe.Pointer(&data[0]))

	if gl.GetError() != gl.NO_ERROR {
		gl.DeleteTextures(1, &id)
		panic("Failed to load a texture")
	}
	return id
}

type Color struct {
	R, G, B, A byte
}

type ColorRange struct {
	Start, End Color
	Range float
}

var (
	DarkYellow = Color{0xEE, 0xEE, 0x9E, 0xFF}
	DarkGreen = Color{0x44, 0x88, 0x44, 0xFF}
	PaleGreyBlue = Color{0x49, 0x93, 0xDD, 0xFF}
	Cyan = Color{0x00, 0xFF, 0xFF, 0xFF}
	Red = Color{0xFF, 0x00, 0x00, 0xFF}
	White = Color{0xFF, 0xFF, 0xFF, 0xFF}
	Black = Color{0x00, 0x00, 0x00, 0xFF}
)

var colorScale = [...]ColorRange{
	ColorRange{DarkYellow, DarkGreen, 0.25},
	ColorRange{DarkGreen, Cyan, 0.25},
	ColorRange{Cyan, Red, 0.25},
	ColorRange{Red, White, 0.125},
	ColorRange{White, PaleGreyBlue, 0.125},
}

var palette []Color

func interpolateColor(c1, c2 Color, f float) Color {
	var c Color
	c.R = byte(float(c1.R) * f + float(c2.R) * (1.0 - f))
	c.G = byte(float(c1.G) * f + float(c2.G) * (1.0 - f))
	c.B = byte(float(c1.B) * f + float(c2.B) * (1.0 - f))
	c.A = byte(float(c1.A) * f + float(c2.A) * (1.0 - f))
	return c
}

func buildPalette() {
	palette = make([]Color, *iterations + 1)
	p := 0
	for _, r := range colorScale {
		n := int(r.Range * float(*iterations) + 0.5)
		for i := 0; i < n && p < *iterations; i++ {
			c := interpolateColor(r.Start, r.End, float(i) / float(n))
			palette[p] = c
			p++
		}
	}
	palette[*iterations] = Black
}

func mandelbrotAt(c complex128) Color {
	var z complex128 = cmplx(0, 0)
	for i := 0; i < *iterations; i++ {
		z = z * z + c
		if real(z) * real(z) + imag(z) * imag(z) > 4 {
			return palette[i]
		}
	}
	return palette[*iterations]
}

type Rect struct {
	X, Y float64
	W, H float64
}

func (self *Rect) Center() (x, y float64) {
	x = self.X + self.W / 2
	y = self.Y + self.H / 2
	return
}

type MandelbrotRequest struct {
	Width int
	Height int
	What Rect
	Discarder <-chan bool
}

func mandelbrotProcessRequest(req *MandelbrotRequest) []byte {
	data := make([]byte, req.Width * req.Height * 4)
	stepx := req.What.W / float64(req.Width)
	stepy := req.What.H / float64(req.Height)

	for y := 0; y < req.Height; y++ {
		i := float64(y) * stepy + req.What.Y

		for x := 0; x < req.Width; x++ {
			r := float64(x) * stepx + req.What.X
			c := cmplx(r, i)

			offset := y * req.Width * 4 + x * 4
			color := mandelbrotAt(c)
			data[offset+0] = color.R
			data[offset+1] = color.G
			data[offset+2] = color.B
			data[offset+3] = color.A
		}
		_, ok := <-req.Discarder
		if ok {
			return nil
		}
	}
	return data

}

func mandelbrotService(in <-chan *MandelbrotRequest) <-chan []byte {
	out := make(chan []byte)
	go func() {
		for {
			request := <-in
			out <- mandelbrotProcessRequest(request)
		}
	}()
	return out
}

//-------------------------------------------------------------------------
// MandelbrotService
//-------------------------------------------------------------------------

type MandelbrotService struct {
	In chan<- *MandelbrotRequest
	Out <-chan []byte
	Tile *Tile // non-nil, means service is busy
	LastRequest *MandelbrotRequest
}

func NewMandelbrotService() *MandelbrotService {
	self := new(MandelbrotService)
	in := make(chan *MandelbrotRequest)
	self.In = in
	self.Out = mandelbrotService(in)
	return self
}

func (self *MandelbrotService) Request(req *MandelbrotRequest, tile *Tile) bool {
	if !self.Busy() {
		self.In <- req
		self.Tile = tile
		self.LastRequest = req
		return true
	}
	return false
}

// returns (data, associated tile) on success
// (nil, nil) on failure
func (self *MandelbrotService) Done() ([]byte, *Tile) {
	if data, ok := <-self.Out; ok {
		t := self.Tile
		self.Tile = nil
		if _, ok := <-self.LastRequest.Discarder; ok {
			return nil, nil
		}
		return data, t
	}
	return nil, nil
}

func (self *MandelbrotService) Busy() bool {
	return self.Tile != nil
}

//-------------------------------------------------------------------------
// MandelbrotQueue
//-------------------------------------------------------------------------

type MandelbrotQueueElem struct {
	Request *MandelbrotRequest
	Tile *Tile
}

type MandelbrotQueue struct {
	Services []*MandelbrotService
	Queue *list.List
}

func NewMandelbrotQueue() *MandelbrotQueue {
	self := new(MandelbrotQueue)
	self.Services = make([]*MandelbrotService, *workers)
	for i, _ := range self.Services {
		self.Services[i] = NewMandelbrotService()
	}
	self.Queue = list.New()
	return self
}

func (self *MandelbrotQueue) Enqueue(w, h int, what Rect, discarder <-chan bool, tile *Tile) {
	r := &MandelbrotRequest{w, h, what, discarder}
	e := &MandelbrotQueueElem{r, tile}
	self.Queue.PushBack(e)
}

func (self *MandelbrotQueue) FreeService() *MandelbrotService {
	// we're ready if the queue is not empty and there is at least one non-busy service
	if self.Queue.Len() == 0 {
		return nil
	}

	for _, s := range self.Services {
		if !s.Busy() {
			return s
		}
	}
	return nil
}

func (self *MandelbrotQueue) Update() {
	for _, s := range self.Services {
		data, tile := s.Done()
		if data != nil {
			tile.ApplyData(data)
		}
	}
	for {
		if s := self.FreeService(); s != nil {
			// pop element from the queue and send it to the service
			front := self.Queue.Front()
			e := front.Value.(*MandelbrotQueueElem)
			self.Queue.Remove(front)
			if _, ok := <-e.Request.Discarder; ok {
				continue
			}
			r := s.Request(e.Request, e.Tile)
			if !r {
				panic("Busy?")
			}
		} else {
			break
		}
	}
}

//-------------------------------------------------------------------------

type Point struct {
	X, Y int
}

func MinInt(i1, i2 int) int {
	if i1 < i2 {
		return i1
	}
	return i2
}

func MaxInt(i1, i2 int) int {
	if i1 > i2 {
		return i1
	}
	return i2
}

func MinFloat64(i1, i2 float64) float64 {
	if i1 < i2 {
		return i1
	}
	return i2
}

func MaxFloat64(i1, i2 float64) float64 {
	if i1 > i2 {
		return i1
	}
	return i2
}

func minMaxPoints(p1, p2 Point) (min, max Point) {
	min.X = MinInt(p1.X, p2.X)
	min.Y = MinInt(p1.Y, p2.Y)
	max.X = MaxInt(p1.X, p2.X)
	max.Y = MaxInt(p1.Y, p2.Y)
	return
}

func drawSelection(p1, p2 Point) {
	min, max := minMaxPoints(p1, p2)

	gl.Color3ub(255,0,0)
	gl.Begin(gl.LINES)
		gl.Vertex2i(gl.GLint(min.X), gl.GLint(min.Y))
		gl.Vertex2i(gl.GLint(max.X), gl.GLint(min.Y))

		gl.Vertex2i(gl.GLint(min.X), gl.GLint(min.Y))
		gl.Vertex2i(gl.GLint(min.X), gl.GLint(max.Y))

		gl.Vertex2i(gl.GLint(max.X), gl.GLint(max.Y))
		gl.Vertex2i(gl.GLint(max.X), gl.GLint(min.Y))

		gl.Vertex2i(gl.GLint(max.X), gl.GLint(max.Y))
		gl.Vertex2i(gl.GLint(min.X), gl.GLint(max.Y))
	gl.End()
	gl.Color3ub(255,255,255)
}

func rectFromSelection(p1, p2 Point, scrw, scrh int, cur Rect) Rect {
	min, max := minMaxPoints(p1, p2)

	// we need to keep aspect ratio here, assuming 1:1 TODO: don't assume!
	cw, ch := max.X - min.X, max.Y - min.Y
	if cw < ch {
		dif := (ch - cw) / 2
		min.X -= dif
		max.X += dif
	} else if ch < cw {
		dif := (cw - ch) / 2
		min.Y -= dif
		max.Y += dif
	}

	stepx := cur.W / float64(scrw)
	stepy := cur.H / float64(scrh)

	var r Rect
	r.X = float64(min.X) * stepx + cur.X
	r.Y = float64(min.Y) * stepy + cur.Y
	r.W = float64(max.X - min.X) * stepx
	r.H = float64(max.Y - min.Y) * stepy
	return r
}

type TexCoords struct {
	TX, TY, TX2, TY2 float
}

func texCoordsFromSelection(p1, p2 Point, w, h int, tcold TexCoords) (tc TexCoords) {
	min, max := minMaxPoints(p1, p2)
	cw, ch := max.X - min.X, max.Y - min.Y
	if cw < ch {
		dif := (ch - cw) / 2
		min.X -= dif
		max.X += dif
	} else if ch < cw {
		dif := (cw - ch) / 2
		min.Y -= dif
		max.Y += dif
	}

	modx := tcold.TX2 - tcold.TX
	mody := tcold.TY2 - tcold.TY

	stepx := (1 / float(w)) * modx
	stepy := (1 / float(h)) * mody

	tc.TX = tcold.TX + float(min.X) * stepx
	tc.TX2 = tcold.TX + float(max.X) * stepx
	tc.TY = tcold.TY + float(min.Y) * stepy
	tc.TY2 = tcold.TY + float(max.Y) * stepy
	return
}

func reuploadTexture(tex *gl.GLuint, w, h int, data []byte) {
	if *tex > 0 {
		gl.BindTexture(gl.TEXTURE_2D, *tex)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.GLsizei(w), gl.GLsizei(h), 0, gl.RGBA,
			      gl.UNSIGNED_BYTE, unsafe.Pointer(&data[0]))

		if gl.GetError() != gl.NO_ERROR {
			gl.DeleteTextures(1, tex)
			panic("Failed to reupload texture")
		}
		return
	}
	*tex = uploadTexture_RGBA32(w, h, data)
}

//-------------------------------------------------------------------------

type Tile struct {
	Texture [2]gl.GLuint	// two LODs
	CurrentLOD int		// -1 if no texture available

	W, H int
	SW, SH int // small LOD

	What Rect
	X, Y int

	// goroutine communication
	Discarder chan bool
	Queue *MandelbrotQueue
	Enqueued bool
}

func NewTile(w, h, sw, sh int, queue *MandelbrotQueue) *Tile {
	self := new(Tile)
	self.CurrentLOD = -1
	self.W, self.H = w, h
	self.SW, self.SH = sw, sh
	self.Queue = queue
	self.Enqueued = false
	return self
}

func (self *Tile) Reset() {
	if self.Enqueued {
		self.Discarder <- true
		self.Enqueued = false
	}
	self.CurrentLOD = -1
}

func (self *Tile) Request(x, y int, what Rect) {
	if self.Enqueued {
		self.Discarder <- true
	}
	self.CurrentLOD = -1
	self.X, self.Y = x, y
	self.What = what
	self.Discarder = make(chan bool, 1)
	self.Enqueued = true
	self.Queue.Enqueue(self.SW, self.SH, what, self.Discarder, self)
}

func (self *Tile) ApplyData(data []byte) {
	switch self.CurrentLOD {
	case -1:
		reuploadTexture(&self.Texture[0], self.SW, self.SH, data)
		self.CurrentLOD = 0
		self.Queue.Enqueue(self.W, self.H, self.What, self.Discarder, self)
	case 0:
		reuploadTexture(&self.Texture[1], self.W, self.H, data)
		self.CurrentLOD = 1
		self.Enqueued = false
	case 1:
		panic("unreachable")
	default:
		panic("unreachable")
	}
}

func (self *Tile) Draw() {
	switch self.CurrentLOD {
	case -1:
		// TODO: draw single color
		r, i := self.What.Center()
		c := cmplx(r, i)
		color := mandelbrotAt(c)
		gl.BindTexture(gl.TEXTURE_2D, 0)
		gl.Color3ub(gl.GLubyte(color.R), gl.GLubyte(color.G), gl.GLubyte(color.B))
		drawQuad(self.X, self.Y, self.W, self.H, 0, 0, 1, 1)
		gl.Color3ub(255, 255, 255)
	case 0:
		gl.BindTexture(gl.TEXTURE_2D, self.Texture[0])
		drawQuad(self.X, self.Y, self.W, self.H, 0, 0, 1, 1)
	case 1:
		gl.BindTexture(gl.TEXTURE_2D, self.Texture[1])
		drawQuad(self.X, self.Y, self.W, self.H, 0, 0, 1, 1)
	default:
		panic("unreachable")
	}
}

//-------------------------------------------------------------------------

type TileManager struct {
	Queue *MandelbrotQueue
	Tiles []*Tile
	LastWhat Rect
	W, H int // screen size
	Div int
	TilePW, TilePH int
}

func NewTileManager(w, h int) *TileManager {
	self := new(TileManager)
	self.Queue = NewMandelbrotQueue()
	self.W, self.H = w, h
	self.Div = *tilesDiv
	self.Tiles = make([]*Tile, (self.Div+1)*(self.Div+1))

	self.TilePW, self.TilePH = w / self.Div, h / self.Div
	sw, sh := self.TilePW / 4, self.TilePH / 4

	for i := 0; i < len(self.Tiles); i++ {
		self.Tiles[i] = NewTile(self.TilePW, self.TilePH, sw, sh, self.Queue)
	}
	self.Tiles = self.Tiles[0:0]
	return self
}

func (self *TileManager) ZoomRequest(what *Rect) {
	// free all tiles
	self.LastWhat = *what
	self.Tiles = self.Tiles[0:0]

	tilew := what.W / float64(self.Div)
	tileh := what.H / float64(self.Div)
	pixw := tilew / float64(self.TilePW)
	pixh := tileh / float64(self.TilePH)

	ox := what.X - (-1.5)
	oy := what.Y - (-1.5)

	tx1 := int(ox / tilew)
	ty1 := int(oy / tileh)
	tx2 := int((ox + what.W) / tilew)
	ty2 := int((oy + what.H) / tileh)
	if tx1 == -2147483648 {
		fmt.Printf("Too close, sorry, zooming out...\n")
		*what = Rect{-1.5,-1.5,3,3}
		self.ZoomRequest(what)
		return
	}

	total := (tx2+1 - tx1) * (ty2+1 - ty1)
	if total > cap(self.Tiles) {
		panic("Total amount of tiles required exceeds total amount of tiles available")
	}

	// allocate tiles
	self.Tiles = self.Tiles[0:total]

	i := 0
	for y := ty1; y <= ty2; y++ {
		for x := tx1; x <= tx2; x++ {
			var r Rect
			r.X = float64(x) * tilew + (-1.5)
			r.Y = float64(y) * tileh + (-1.5)
			r.W = tilew
			r.H = tileh
			px := int((r.X - what.X) / pixw)
			py := int((r.Y - what.Y) / pixh)
			self.Tiles[i].Request(px, py, r)
			i++
		}
	}
}

func overlaps(r1, r2 Rect) bool {
	maxX := MaxFloat64(r1.X, r2.X)
	minX := MinFloat64(r1.X + r1.W, r2.X + r2.W)
	ix := minX > maxX

	maxY := MaxFloat64(r1.Y, r2.Y)
	minY := MinFloat64(r1.Y + r1.H, r2.Y + r2.H)
	iy := minY > maxY
	return ix && iy
}

func (self *TileManager) MoveRequest(what Rect) {
	// in move request we have to be careful with tiles, 
	// because some of them are pretty valid
	tilew := what.W / float64(self.Div)
	tileh := what.H / float64(self.Div)
	pixw := tilew / float64(self.TilePW)
	pixh := tileh / float64(self.TilePH)

	for i := 0; i < len(self.Tiles); i++ {
		t := self.Tiles[i]
		if overlaps(t.What, what) {
			t.X = int((t.What.X - what.X) / pixw)
			t.Y = int((t.What.Y - what.Y) / pixh)
		} else {
			self.Tiles[i].Reset()
			last := len(self.Tiles) - 1
			self.Tiles[i], self.Tiles[last] = self.Tiles[last], self.Tiles[i]
			self.Tiles = self.Tiles[0:last]
			i--
		}
	}

	ox := what.X - (-1.5)
	oy := what.Y - (-1.5)

	tx1 := int(ox / tilew)
	ty1 := int(oy / tileh)
	tx2 := int((ox + what.W) / tilew)
	ty2 := int((oy + what.H) / tileh)

	total := (tx2+1 - tx1) * (ty2+1 - ty1)
	if total > cap(self.Tiles) {
		panic("Total amount of tiles required exceeds total amount of tiles available")
	}

	i := len(self.Tiles)
	// allocate tiles
	self.Tiles = self.Tiles[0:total]

	for y := ty1; y <= ty2; y++ {
		for x := tx1; x <= tx2; x++ {
			var r Rect
			r.X = float64(x) * tilew + (-1.5)
			r.Y = float64(y) * tileh + (-1.5)
			r.W = tilew
			r.H = tileh
			if !overlaps(r, self.LastWhat) {
				// this tile was newly introduced
				px := int((r.X - what.X) / pixw)
				py := int((r.Y - what.Y) / pixh)
				if i >= len(self.Tiles) {
					fmt.Printf("missing free tiles: %f %f %f %f (%f %f %f %f) [%d]\n",
						   r.X, r.Y, r.W, r.H,
						   what.X, what.Y, what.W, what.H, i)
				} else {
					self.Tiles[i].Request(px, py, r)
				}
				i++
			}
		}
	}
	self.LastWhat = what
}

func (self *TileManager) Update() {
	self.Queue.Update()
}

func (self *TileManager) Draw() {
	for i := 0; i < len(self.Tiles); i++ {
		self.Tiles[i].Draw()
	}
}

func moveRectBy(what Rect, e, s Point, w, h int) Rect {
	pixw := what.W / float64(w)
	pixh := what.H / float64(h)

	var r Rect
	r.X = what.X + float64(e.X - s.X) * pixw
	r.Y = what.Y + float64(e.Y - s.Y) * pixh
	r.W = what.W
	r.H = what.H
	if r.X < -1.5 || r.Y < -1.5 || (r.X + r.W) > 1.5 || (r.Y + r.H) > 1.5 {
		return what
	}
	return r
}

//-------------------------------------------------------------------------
// main()
//-------------------------------------------------------------------------

func main() {
	runtime.LockOSThread()
	flag.Parse()
	if *workers <= 0 {
		*workers = 1
	}
	buildPalette()
	sdl.Init(sdl.INIT_VIDEO)
	defer sdl.Quit()

	if !*noVSync {
		sdl.GL_SetAttribute(sdl.GL_SWAP_CONTROL, 1)
	}

	if sdl.SetVideoMode(512, 512, 32, sdl.OPENGL) == nil {
		panic("sdl error")
	}

	sdl.WM_SetCaption("Gomandel", "Gomandel")

	if gl.Init() != 0 {
		panic("glew error")
	}

	gl.Enable(gl.TEXTURE_2D)
	gl.Viewport(0, 0, 512, 512)
	gl.MatrixMode(gl.PROJECTION)
	gl.LoadIdentity()
	gl.Ortho(0, 512, 512, 0, -1, 1)

	gl.ClearColor(0, 0, 0, 0)


	//-----------------------------------------------------------------------------
	var dndDragging bool = false
	var dnd3 bool = false
	var dndStart Point
	var dndEnd Point
	initialRect := Rect{-1.5,-1.5,3,3}
	rect := initialRect

	tm := NewTileManager(512, 512)
	tm.ZoomRequest(&rect)

	running := true

	e := sdl.Event{}
	for running {
		for e.Poll() {
			switch e.Type {
			case sdl.QUIT:
				running = false
			case sdl.MOUSEBUTTONDOWN:
				dndDragging = true
				dndStart.X = int(e.MouseButton().X)
				dndStart.Y = int(e.MouseButton().Y)
				dndEnd = dndStart
				if e.MouseButton().Button == 3 {
					dnd3 = true
				} else {
					dndDragging = true
				}
			case sdl.MOUSEBUTTONUP:
				dndDragging = false
				dndEnd.X = int(e.MouseButton().X)
				dndEnd.Y = int(e.MouseButton().Y)

				switch e.MouseButton().Button {
				case 1:
					rect = rectFromSelection(dndStart, dndEnd, 512, 512, rect)
					tm.ZoomRequest(&rect)
				case 2:
					rect = initialRect
					tm.ZoomRequest(&rect)
				case 3:
					dnd3 = false
				}
			case sdl.MOUSEMOTION:
				if dnd3 {
					dndEnd.X = int(e.MouseMotion().X)
					dndEnd.Y = int(e.MouseMotion().Y)
					rect = moveRectBy(rect, dndStart, dndEnd, 512, 512)
					tm.MoveRequest(rect)
					dndStart = dndEnd
				} else if dndDragging {
					dndEnd.X = int(e.MouseMotion().X)
					dndEnd.Y = int(e.MouseMotion().Y)
				}
			}
		}
		tm.Update()
		gl.Clear(gl.COLOR_BUFFER_BIT)
		tm.Draw()
		gl.BindTexture(gl.TEXTURE_2D, 0)
		if dndDragging {
			drawSelection(dndStart, dndEnd)
		}
		sdl.GL_SwapBuffers()
	}
}
