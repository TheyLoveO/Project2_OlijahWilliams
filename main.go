package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	_ "image/png"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

//
// ----------------------------------------------------------------------
// EMBEDDED ASSETS
// ----------------------------------------------------------------------
//

// tilesheet + TMX maps
//
//go:embed assets/tiles/0x72_16x16DungeonTileset.v5.png
var tilesheetPNG []byte

//go:embed assets/tiles/Level1.tmx
var level1TMX []byte

//go:embed assets/tiles/level2.tmx
var level2TMX []byte

// player sprite
//
//go:embed assets/sprites/npc_knight_yellow.png
var playerKnightPNG []byte

// door
//
//go:embed assets/sprites/door_closed.png
var doorClosedPNG []byte

// good items
//
//go:embed assets/sprites/flask_big_red.png
var flaskRedPNG []byte

//go:embed assets/sprites/flask_big_blue.png
var flaskBluePNG []byte

//go:embed assets/sprites/flask_big_yellow.png
var flaskYellowPNG []byte

// bad items
//
//go:embed assets/sprites/weapon_bomb.png
var weaponBombPNG []byte

//go:embed assets/sprites/weapon_chopper.png
var weaponChopperPNG []byte

//go:embed assets/sprites/weapon_dagger_silver.png
var weaponDaggerSilverPNG []byte

//go:embed assets/sprites/weapon_dagger_small.png
var weaponDaggerSmallPNG []byte

// NPC sprites
//
//go:embed assets/sprites/npc_merchant.png
var npcMerchantPNG []byte

//go:embed assets/sprites/npc_merchant_2.png
var npcMerchant2PNG []byte

//go:embed assets/sprites/npc_paladin.png
var npcPaladinPNG []byte

//go:embed assets/sprites/npc_sage.png
var npcSagePNG []byte

//go:embed assets/sprites/npc_trickster.png
var npcTricksterPNG []byte

//go:embed assets/sprites/npc_wizzard.png
var npcWizzardPNG []byte

//go:embed assets/sprites/npc_elf_2.png
var npcElf2PNG []byte

//
// ----------------------------------------------------------------------
// CONSTANTS
// ----------------------------------------------------------------------
//

// tilesheet tiles are 16x16
const tilesetTileSize = 16

// we draw them 1:1 (no scaling inside Ebiten)
const tileSize = 16

const (
	mapWidth  = 20
	mapHeight = 20

	screenWidth  = mapWidth * tileSize
	screenHeight = mapHeight * tileSize

	itemsNeeded = 9 // how many good items to unlock the door
)

//
// ----------------------------------------------------------------------
// BASIC TYPES (MAP, PLAYER, ITEMS, NPCs)
// ----------------------------------------------------------------------
//

// TileMap holds the tile IDs from Tiled
type TileMap struct {
	Width   int
	Height  int
	Tiles   [][]int      // each entry is a global tile ID from TMX
	WallGID map[int]bool // which tile IDs are walls (we leave this empty)
}

// Player is the controllable knight
type Player struct {
	X, Y      float64
	Speed     float64
	Dir       string // "up", "down", "left", "right"
	Frame     int
	FrameTick int
	Frames    map[string][]*ebiten.Image // animation frames per direction
}

// Item is either good (potion) or bad (weapon/bomb)
type Item struct {
	X, Y   float64
	Bad    bool
	Active bool
	Img    *ebiten.Image
}

// NPC just walks back and forth (or around) in level 2
type NPC struct {
	X, Y       float64
	VX, VY     float64
	MinX, MaxX float64
	MinY, MaxY float64
	Img        *ebiten.Image
}

// Game holds all state
type Game struct {
	level int

	map1 *TileMap
	map2 *TileMap

	tileImages []*ebiten.Image

	player Player

	// items
	goodItemImages []*ebiten.Image
	badItemImages  []*ebiten.Image
	goodItems      []Item
	badItems       []Item

	// door to next level
	doorImage   *ebiten.Image
	doorX       float64
	doorY       float64
	doorVisible bool

	// NPCs on level 2
	npcImages []*ebiten.Image
	npcs      []NPC

	collected int
	levelGoal int // 9

	gameOver bool
	msg      string // status line (e.g., "hit bad item")
}

//
// ----------------------------------------------------------------------
// SMALL HELPERS
// ----------------------------------------------------------------------
//

// decode PNG bytes -> ebiten.Image
func loadSprite(pngBytes []byte) *ebiten.Image {
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		log.Fatalf("failed to decode sprite: %v", err)
	}
	return ebiten.NewImageFromImage(img)
}

// load several PNGs at once
func loadSprites(pngs ...[]byte) []*ebiten.Image {
	res := make([]*ebiten.Image, 0, len(pngs))
	for _, b := range pngs {
		res = append(res, loadSprite(b))
	}
	return res
}

// distance squared between two points, used for collisions
func dist2(x1, y1, x2, y2 float64) float64 {
	dx := x1 - x2
	dy := y1 - y2
	return dx*dx + dy*dy
}

//
// ----------------------------------------------------------------------
// LOAD TILESET
// ----------------------------------------------------------------------
//

// cut the 16x16 tilesheet into 16x16 ebiten images (no scaling)
func loadTilesheet() []*ebiten.Image {
	img, _, err := image.Decode(bytes.NewReader(tilesheetPNG))
	if err != nil {
		log.Fatalf("failed to decode tilesheet: %v", err)
	}

	var tiles []*ebiten.Image

	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	for y := 0; y < h; y += tilesetTileSize {
		for x := 0; x < w; x += tilesetTileSize {
			// source 16x16 tile from the sheet (standard image.Image)
			sub := img.(interface {
				SubImage(r image.Rectangle) image.Image
			}).SubImage(image.Rect(x, y, x+tilesetTileSize, y+tilesetTileSize))

			// upload to GPU as an ebiten.Image
			tiles = append(tiles, ebiten.NewImageFromImage(sub))
		}
	}

	fmt.Println("tilesheet loaded, tiles:", len(tiles))
	return tiles
}

//
// ----------------------------------------------------------------------
// TMX PARSING (very small, only width/height + CSV data)
// ----------------------------------------------------------------------
//

// pull width="20" style attribute out of the TMX text
func extractIntAttr(text, key string) int {
	i := strings.Index(text, key)
	if i < 0 {
		return 0
	}
	i += len(key)
	j := strings.Index(text[i:], "\"")
	if j < 0 {
		return 0
	}
	n, _ := strconv.Atoi(text[i : i+j])
	return n
}

// parseTMX reads width/height and the CSV layer into TileMap.Tiles
func parseTMX(data []byte) *TileMap {
	text := string(data)

	tm := &TileMap{
		WallGID: make(map[int]bool),
	}

	tm.Width = extractIntAttr(text, `width="`)
	tm.Height = extractIntAttr(text, `height="`)

	// find <data> ... </data>
	dataStart := strings.Index(text, "<data")
	if dataStart < 0 {
		log.Fatal("TMX: no <data> tag")
	}
	dataStart = strings.Index(text[dataStart:], ">") + dataStart + 1
	dataEnd := strings.Index(text[dataStart:], "</data>")
	if dataEnd < 0 {
		log.Fatal("TMX: no </data> tag")
	}

	csv := strings.TrimSpace(text[dataStart : dataStart+dataEnd])

	rows := strings.Split(csv, "\n")
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		parts := strings.Split(row, ",")
		line := make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, _ := strconv.Atoi(p)
			line = append(line, n)
		}
		if len(line) > 0 {
			tm.Tiles = append(tm.Tiles, line)
		}
	}

	// IMPORTANT: we leave WallGID empty so *nothing* is a wall for now.
	// isWallAtPixel only blocks you from leaving the map.
	return tm
}

// ----------------------------------------------------------------------
// COLLISION
// ----------------------------------------------------------------------
//
// isWallAtPixel checks if a pixel location is outside the map or hits
// a tile whose ID is marked as a wall in tm.WallGID.
func isWallAtPixel(tm *TileMap, px, py float64) bool {
	if tm == nil || tm.Width <= 0 || tm.Height <= 0 || len(tm.Tiles) == 0 {
		return false
	}

	// Convert pixel position to tile indices.
	tx := int(px) / tileSize
	ty := int(py) / tileSize

	// If out of bounds, treat as a wall.
	if tx < 0 || ty < 0 || tx >= tm.Width || ty >= tm.Height {
		return true
	}

	// Force the *outer ring* of tiles to be solid walls.
	// This guarantees the player can NEVER walk off the visible map.
	if tx == 0 || ty == 0 || tx == tm.Width-1 || ty == tm.Height-1 {
		return true
	}

	if ty < 0 || ty >= len(tm.Tiles) {
		return false
	}
	row := tm.Tiles[ty]
	if tx < 0 || tx >= len(row) {
		return false
	}

	gid := row[tx]

	// If this tile ID is in WallGID, it's solid.
	if tm.WallGID[gid] {
		return true
	}
	return false
}

//
// ----------------------------------------------------------------------
// SPAWN ITEMS
// ----------------------------------------------------------------------
//

func spawnItems(tm *TileMap, bad bool, images []*ebiten.Image) []Item {
	items := []Item{}
	if tm == nil || tm.Width <= 0 || tm.Height <= 0 || len(tm.Tiles) == 0 {
		fmt.Println("WARNING: spawnItems called with empty map")
		return items
	}
	if len(images) == 0 {
		fmt.Println("WARNING: spawnItems has no images")
		return items
	}

	target := 15 // professor wants 15+ good items; we still only *need* 9
	if bad {
		target = 5
	}

	maxTries := target * 500
	tries := 0

	for len(items) < target && tries < maxTries {
		tries++

		x := rand.Intn(tm.Width)
		y := rand.Intn(len(tm.Tiles))

		row := tm.Tiles[y]
		if x < 0 || x >= len(row) {
			continue
		}

		gid := row[x]
		// If we ever mark walls, skip them. Right now WallGID is empty.
		if tm.WallGID[gid] {
			continue
		}

		img := images[rand.Intn(len(images))]

		items = append(items, Item{
			X:      float64(x * tileSize),
			Y:      float64(y * tileSize),
			Bad:    bad,
			Active: true,
			Img:    img,
		})
	}

	if len(items) < target {
		fmt.Println("WARNING: only spawned", len(items), "of", target, "items (map might be mostly walls)")
	}

	return items
}

//
// ----------------------------------------------------------------------
// PLAYER SPRITES
// ----------------------------------------------------------------------
//

// We only have one knight frame, so we just use it for all directions.
func loadPlayerFrames() map[string][]*ebiten.Image {
	img := loadSprite(playerKnightPNG)

	return map[string][]*ebiten.Image{
		"down":  {img},
		"up":    {img},
		"left":  {img},
		"right": {img},
	}
}

//
// ----------------------------------------------------------------------
// GAME CONSTRUCTION
// ----------------------------------------------------------------------
//

func newGame() *Game {
	fmt.Println(">>> newGame() start")

	g := &Game{
		levelGoal: itemsNeeded,
	}

	g.tileImages = loadTilesheet()
	g.map1 = parseTMX(level1TMX)
	g.map2 = parseTMX(level2TMX)

	fmt.Printf("map1 size: %d x %d tiles rows: %d\n", g.map1.Width, g.map1.Height, len(g.map1.Tiles))
	fmt.Printf("map2 size: %d x %d tiles rows: %d\n", g.map2.Width, g.map2.Height, len(g.map2.Tiles))

	// player
	g.player = Player{
		Frames: loadPlayerFrames(),
		Speed:  2.0,
		Dir:    "down",
	}

	// items
	g.goodItemImages = loadSprites(flaskRedPNG, flaskBluePNG, flaskYellowPNG)
	g.badItemImages = loadSprites(weaponBombPNG, weaponChopperPNG, weaponDaggerSilverPNG, weaponDaggerSmallPNG)

	// door + NPCs
	g.doorImage = loadSprite(doorClosedPNG)
	g.npcImages = loadSprites(
		npcMerchantPNG,
		npcMerchant2PNG,
		npcPaladinPNG,
		npcSagePNG,
		npcTricksterPNG,
		npcWizzardPNG,
		npcElf2PNG,
	)

	g.setupLevel(1)

	fmt.Println(">>> newGame() done")
	return g
}

func (g *Game) currentMap() *TileMap {
	if g.level == 1 {
		return g.map1
	}
	return g.map2
}

func (g *Game) setupLevel(level int) {
	g.level = level
	g.collected = 0
	g.gameOver = false
	g.msg = ""
	g.doorVisible = false
	g.npcs = nil

	tm := g.currentMap()
	if tm == nil {
		return
	}

	// spawn player somewhere safe-ish (2,2)
	g.player.X = float64(2 * tileSize)
	g.player.Y = float64(2 * tileSize)
	g.player.Dir = "down"
	g.player.Frame = 0
	g.player.FrameTick = 0

	// spawn items
	g.goodItems = spawnItems(tm, false, g.goodItemImages)
	g.badItems = spawnItems(tm, true, g.badItemImages)

	// level 2 gets NPCs
	if level == 2 {
		g.setupNPCs()
	}
}

// create 7 NPCs walking in different patterns
func (g *Game) setupNPCs() {
	tm := g.currentMap()
	if tm == nil || len(g.npcImages) == 0 {
		return
	}

	g.npcs = nil

	tile := func(tx, ty int) (float64, float64) {
		return float64(tx * tileSize), float64(ty * tileSize)
	}

	add := func(idx, tx, ty int, vx, vy float64, minTx, maxTx, minTy, maxTy int) {
		img := g.npcImages[idx%len(g.npcImages)]
		x, y := tile(tx, ty)
		g.npcs = append(g.npcs, NPC{
			X:    x,
			Y:    y,
			VX:   vx,
			VY:   vy,
			MinX: float64(minTx * tileSize),
			MaxX: float64(maxTx * tileSize),
			MinY: float64(minTy * tileSize),
			MaxY: float64(maxTy * tileSize),
			Img:  img,
		})
	}

	// horizontal, vertical and diagonal motions
	add(0, 5, 5, 1.0, 0.0, 4, 10, 5, 5)
	add(1, 10, 8, -1.2, 0.0, 5, 12, 8, 8)
	add(2, 7, 12, 0.0, 1.0, 7, 7, 10, 16)
	add(3, 12, 6, 0.0, -1.1, 12, 12, 4, 14)
	add(4, 3, 14, 0.8, 0.8, 2, 8, 13, 18)
	add(5, 15, 10, -0.8, 0.8, 12, 18, 8, 16)
	add(6, 9, 3, 0.0, 1.3, 9, 9, 2, 15)
}

//
// ----------------------------------------------------------------------
// UPDATE HELPERS
// ----------------------------------------------------------------------
//

// keyboard + basic animation
func (g *Game) updatePlayer() {
	tm := g.currentMap()
	if tm == nil {
		return
	}

	dx, dy := 0.0, 0.0

	if ebiten.IsKeyPressed(ebiten.KeyLeft) || ebiten.IsKeyPressed(ebiten.KeyA) {
		dx -= g.player.Speed
		g.player.Dir = "left"
	}
	if ebiten.IsKeyPressed(ebiten.KeyRight) || ebiten.IsKeyPressed(ebiten.KeyD) {
		dx += g.player.Speed
		g.player.Dir = "right"
	}
	if ebiten.IsKeyPressed(ebiten.KeyUp) || ebiten.IsKeyPressed(ebiten.KeyW) {
		dy -= g.player.Speed
		g.player.Dir = "up"
	}
	if ebiten.IsKeyPressed(ebiten.KeyDown) || ebiten.IsKeyPressed(ebiten.KeyS) {
		dy += g.player.Speed
		g.player.Dir = "down"
	}

	moving := dx != 0 || dy != 0

	if moving {
		// X movement
		newX := g.player.X + dx
		if !isWallAtPixel(tm, newX+float64(tileSize)/2, g.player.Y+float64(tileSize)/2) {
			g.player.X = newX
		}

		// Y movement
		newY := g.player.Y + dy
		if !isWallAtPixel(tm, g.player.X+float64(tileSize)/2, newY+float64(tileSize)/2) {
			g.player.Y = newY
		}

		// fake animation counter
		g.player.FrameTick++
		if g.player.FrameTick >= 8 {
			g.player.FrameTick = 0
			frames := g.player.Frames[g.player.Dir]
			if len(frames) > 0 {
				g.player.Frame = (g.player.Frame + 1) % len(frames)
			}
		}
	} else {
		g.player.Frame = 0
		g.player.FrameTick = 0
	}
}

// pick up potions, hit bombs, etc.
func (g *Game) updateItems() {
	px := g.player.X + float64(tileSize)/2
	py := g.player.Y + float64(tileSize)/2
	r2 := float64(tileSize) * float64(tileSize) * 0.5

	// good items
	for i := range g.goodItems {
		it := &g.goodItems[i]
		if !it.Active {
			continue
		}
		if dist2(px, py, it.X+float64(tileSize)/2, it.Y+float64(tileSize)/2) < r2 {
			it.Active = false
			g.collected++
			if g.collected >= g.levelGoal && !g.doorVisible {
				g.spawnDoor()
			}
		}
	}

	// bad items
	for i := range g.badItems {
		it := &g.badItems[i]
		if !it.Active {
			continue
		}
		if dist2(px, py, it.X+float64(tileSize)/2, it.Y+float64(tileSize)/2) < r2 {
			g.gameOver = true
			g.msg = "You hit a bad item! Press SPACE to restart."
			return
		}
	}
}

func (g *Game) spawnDoor() {
	tm := g.currentMap()
	if tm == nil {
		return
	}
	// simple: bottom-right corner
	x := tm.Width - 2
	y := tm.Height - 2
	g.doorX = float64(x * tileSize)
	g.doorY = float64(y * tileSize)
	g.doorVisible = true
}

func (g *Game) updateDoor() {
	if !g.doorVisible {
		return
	}

	px := g.player.X + float64(tileSize)/2
	py := g.player.Y + float64(tileSize)/2
	r2 := float64(tileSize) * float64(tileSize) * 0.5

	if dist2(px, py, g.doorX+float64(tileSize)/2, g.doorY+float64(tileSize)/2) < r2 {
		// player reached the door
		if g.level == 1 {
			g.setupLevel(2)
		} else {
			g.gameOver = true
			g.msg = "You escaped the dungeon! Press SPACE to restart."
		}
	}
}

func (g *Game) updateNPCs() {
	for i := range g.npcs {
		npc := &g.npcs[i]

		npc.X += npc.VX
		npc.Y += npc.VY

		if npc.X < npc.MinX {
			npc.X = npc.MinX
			npc.VX = -npc.VX
		} else if npc.X > npc.MaxX {
			npc.X = npc.MaxX
			npc.VX = -npc.VX
		}

		if npc.Y < npc.MinY {
			npc.Y = npc.MinY
			npc.VY = -npc.VY
		} else if npc.Y > npc.MaxY {
			npc.Y = npc.MaxY
			npc.VY = -npc.VY
		}
	}
}

//
// ----------------------------------------------------------------------
// ebiten.Game IMPLEMENTATION
// ----------------------------------------------------------------------
//

func (g *Game) Update() error {
	// game over: wait for SPACE to reset
	if g.gameOver {
		if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
			if strings.Contains(g.msg, "escaped") {
				g.setupLevel(1)
			} else {
				g.setupLevel(g.level)
			}
			g.gameOver = false
			g.msg = ""
		}
		return nil
	}

	// quick manual reset
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.setupLevel(g.level)
	}

	g.updatePlayer()
	g.updateItems()
	g.updateDoor()
	if g.level == 2 {
		g.updateNPCs()
	}

	return nil
}

func (g *Game) drawMap(screen *ebiten.Image) {
	tm := g.currentMap()
	if tm == nil {
		return
	}

	for y, row := range tm.Tiles {
		for x, gid := range row {
			if gid <= 0 {
				continue
			}
			idx := gid - 1
			if idx < 0 || idx >= len(g.tileImages) {
				continue
			}
			op := &ebiten.DrawImageOptions{}
			op.GeoM.Translate(float64(x*tileSize), float64(y*tileSize))
			screen.DrawImage(g.tileImages[idx], op)
		}
	}
}

func (g *Game) drawItems(screen *ebiten.Image) {
	for _, it := range g.goodItems {
		if !it.Active {
			continue
		}
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(it.X, it.Y)
		screen.DrawImage(it.Img, op)
	}
	for _, it := range g.badItems {
		if !it.Active {
			continue
		}
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(it.X, it.Y)
		screen.DrawImage(it.Img, op)
	}
}

func (g *Game) drawNPCs(screen *ebiten.Image) {
	for _, npc := range g.npcs {
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(npc.X, npc.Y)
		screen.DrawImage(npc.Img, op)
	}
}

func (g *Game) drawDoor(screen *ebiten.Image) {
	if !g.doorVisible || g.doorImage == nil {
		return
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(g.doorX, g.doorY)
	screen.DrawImage(g.doorImage, op)
}

func (g *Game) Draw(screen *ebiten.Image) {
	g.drawMap(screen)
	g.drawItems(screen)
	g.drawDoor(screen)
	g.drawNPCs(screen)

	// draw player last so they appear on top
	frames := g.player.Frames[g.player.Dir]
	img := frames[0]
	if len(frames) > 0 {
		img = frames[g.player.Frame%len(frames)]
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(g.player.X, g.player.Y)
	screen.DrawImage(img, op)

	// simple HUD at top-left
	hud := fmt.Sprintf("Level: %d   Items: %d / %d", g.level, g.collected, g.levelGoal)
	if g.msg != "" {
		hud += "\n" + g.msg
	}
	ebitenutil.DebugPrint(screen, hud)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

//
// ----------------------------------------------------------------------
// MAIN
// ----------------------------------------------------------------------
//

func main() {
	log.SetFlags(0)
	fmt.Println(">>> main() starting")

	rand.Seed(time.Now().UnixNano())

	game := newGame()

	// window is same as logical size, so tiles fill the whole thing
	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Project 2 - Olijah Williams")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
