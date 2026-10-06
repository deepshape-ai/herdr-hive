package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

var requiredCodecs = []struct{ field, codec string }{
	{"snapshot", "shell.snapshot.v1"},
	{"surface", "shell.surface.v1"},
	{"input", "shell.input.semantic.v1"},
	{"blob", "shell.blob.v1"},
}

func welcomeValue() map[string]any {
	v := map[string]any{"generation": 1, "server_version": "0.9.0", "methods": supportedMethods, "capabilities": []string{"surface_interest", "presentation_effects_fence", "health_check"}}
	for _, c := range requiredCodecs {
		v[c.field+"_codec"] = c.codec
	}
	return v
}

func helloInteger(v any, max uint64) (uint64, bool) {
	n, ok := v.(float64)
	if !ok || n < 0 || n > float64(max) || math.Trunc(n) != n {
		return 0, false
	}
	return uint64(n), true
}

// The gateway is a protocol endpoint, not a transparent viewer. Construct its
// own offer; copying a future viewer flag would negotiate a codec we cannot read.
func (g *session) negotiateHello(data string) error {
	var h map[string]any
	if len(data) > 256<<10 || json.Unmarshal([]byte(data), &h) != nil || h == nil {
		return errors.New("invalid endpoint hello JSON")
	}
	if h["generation"] != float64(1) {
		return errors.New("unsupported endpoint generation: Hive requires generation 1")
	}
	upstream := map[string]any{"generation": 1, "direct_graphics": false, "surface_active": false}
	for _, c := range requiredCodecs {
		ok := false
		for _, v := range array(h[c.field+"_codecs"]) {
			ok = ok || v == c.codec
		}
		if !ok {
			return fmt.Errorf("unsupported endpoint codec: Hive requires %s", c.codec)
		}
		upstream[c.field+"_codecs"] = []string{c.codec}
	}
	size := object(h["surface_size"])
	cols, colsOK := helloInteger(size["cols"], 65535)
	rows, rowsOK := helloInteger(size["rows"], 65535)
	width, widthOK := helloInteger(h["cell_width_px"], math.MaxUint32)
	height, heightOK := helloInteger(h["cell_height_px"], math.MaxUint32)
	if !colsOK || !rowsOK || cols == 0 || rows == 0 || cols*rows > 65536 || !widthOK || !heightOK {
		return errors.New("invalid endpoint geometry: positive integer dimensions, at most 65536 cells, and uint32 cell pixels are required")
	}
	for _, field := range []string{"pixel_mouse", "mouse_capture", "endpoint_keybindings", "direct_graphics"} {
		v, ok := h[field].(bool)
		if !ok {
			return fmt.Errorf("invalid endpoint hello: %s must be boolean", field)
		}
		if field != "direct_graphics" {
			upstream[field] = v
		}
	}
	g.surfaceActive = true
	if v, exists := h["surface_active"]; exists {
		active, ok := v.(bool)
		if !ok {
			return errors.New("invalid endpoint hello: surface_active must be boolean")
		}
		g.surfaceActive = active
	}
	g.cols, g.rows = cols, rows
	upstream["cell_width_px"], upstream["cell_height_px"] = width, height
	upstream["surface_size"] = map[string]uint64{"cols": cols, "rows": rows}
	g.resize = append(number(12), number(width)...)
	g.resize = append(g.resize, number(height)...)
	g.resize = append(g.resize, number(cols)...)
	g.resize = append(g.resize, number(rows)...)
	pixelMouse := byte(0)
	if h["pixel_mouse"] == true {
		pixelMouse = 1
	}
	g.resize = append(g.resize, pixelMouse)
	p, err := json.Marshal(upstream)
	if err != nil {
		return err
	}
	g.hello = control("endpoint.hello.v1", p)
	return nil
}

func validateWelcome(v map[string]any) error {
	if v["error"] != nil || v["generation"] != float64(1) {
		return errors.New("publisher rejected generation 1 endpoint handshake")
	}
	for _, c := range requiredCodecs {
		if v[c.field+"_codec"] != c.codec {
			return fmt.Errorf("publisher selected unsupported %s codec", c.field)
		}
	}
	return nil
}
