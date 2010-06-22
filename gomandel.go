package main

import (
	"sdl"
	"gl"
	"unsafe"
	"flag"
	"runtime"
)

var iterations *int = flag.Int("i", 128, "number of iterations")

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
		return 0
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

func mandelbrot(w, h int, what Rect, discard chan bool) <-chan []byte {
	result := make(chan []byte)
	go func() {
		data := make([]byte, w * h * 4)
		stepx := what.W / float64(w)
		stepy := what.H / float64(h)

		for y := 0; y < h; y++ {
			i := float64(y) * stepy + what.Y

			for x := 0; x < w; x++ {
				r := float64(x) * stepx + what.X
				c := cmplx(r, i)

				offset := y * w * 4 + x * 4
				color := mandelbrotAt(c)
				data[offset+0] = color.R
				data[offset+1] = color.G
				data[offset+2] = color.B
				data[offset+3] = color.A
			}
			_, ok := <-discard
			if ok {
				result <- nil
				return
			}
		}
		result <- data
	}()

	return result
}

//-------------------------------------------------------------------------
// main()
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

func main() {
	runtime.LockOSThread()
	flag.Parse()
	buildPalette()
	sdl.Init(sdl.INIT_VIDEO)
	defer sdl.Quit()

	sdl.GL_SetAttribute(sdl.GL_SWAP_CONTROL, 1)

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

	discarder := make(chan bool)
	rect := Rect{-1.5,-1.5,3,3}

	result := mandelbrot(512, 512, rect, discarder)

	data := <-result
	tex := uploadTexture_RGBA32(512, 512, data)

	result = nil

	//-----------------------------------------------------------------------------
	var dndDragging bool = false
	var dndStart Point
	var dndEnd Point
	tc := TexCoords{0,0,1,1}

	running := true
	e := new(sdl.Event)
	for running {
		for e.Poll() {
			switch e.Type {
			case sdl.QUIT:
				running = false
			case sdl.MOUSEBUTTONDOWN:
				dndDragging = true
				sdl.GetMouseState(&dndStart.X, &dndStart.Y)
				dndEnd = dndStart
			case sdl.MOUSEBUTTONUP:
				dndDragging = false
				sdl.GetMouseState(&dndEnd.X, &dndEnd.Y)
				rect = rectFromSelection(dndStart, dndEnd, 512, 512, rect)
				tc = texCoordsFromSelection(dndStart, dndEnd, 512, 512, tc)

				// make a request
				if result != nil {
					// if something is pending, stop it!
					discarder <- true

					// wait for response
					<-result
				}
				result = mandelbrot(512, 512, rect, discarder)
			case sdl.MOUSEMOTION:
				if dndDragging {
					sdl.GetMouseState(&dndEnd.X, &dndEnd.Y)
				}
			}
		}
		// if we're waiting for a result, check if it's ready
		if result != nil {
			data, ok := <-result
			if ok {
				gl.DeleteTextures(1, &tex)
				tex = uploadTexture_RGBA32(512, 512, data)
				result = nil
				tc = TexCoords{0, 0, 1, 1}
			}
		}

		gl.Clear(gl.COLOR_BUFFER_BIT)
		gl.BindTexture(gl.TEXTURE_2D, tex)
		drawQuad(0,0,512,512,tc.TX,tc.TY,tc.TX2,tc.TY2)
		gl.BindTexture(gl.TEXTURE_2D, 0)
		if dndDragging {
			drawSelection(dndStart, dndEnd)
		}
		sdl.GL_SwapBuffers()
	}
}
