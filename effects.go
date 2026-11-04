package main

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nullentropy/caution"
)

var effects = map[string]caution.FX{
	"crt": {Frag: hash + `
vec4 effect(vec2 uv) {
  vec2 px = 1.0 / u_res;
  vec4 c = src(uv);
  // red and blue guns converge half a pixel off
  c.r = src(uv + vec2(0.5 * px.x, 0.0)).r;
  c.b = src(uv - vec2(0.5 * px.x, 0.0)).b;
  vec3 glow = (src(uv + vec2(1.5 * px.x, 0.0)).rgb + src(uv - vec2(1.5 * px.x, 0.0)).rgb +
               src(uv + vec2(0.0, 1.5 * px.y)).rgb + src(uv - vec2(0.0, 1.5 * px.y)).rgb) * 0.25;
  c.rgb += glow * 0.3;
  c.rgb *= 0.8 + 0.2 * sin(uv.y * u_res.y * 3.14159);
  // aperture grille, one stripe per logical pixel
  float stripe = mod(floor(uv.x * u_res.x), 3.0);
  c.rgb *= mix(vec3(0.88), vec3(1.0), vec3(stripe == 0.0, stripe == 1.0, stripe == 2.0));
  vec2 d = uv * 2.0 - 1.0;
  c.rgb *= 1.0 - 0.18 * dot(d, d);
  c.rgb *= 1.18;
  return c;
}`},

	"vhs": {Animate: true, Frag: hash + `
vec4 effect(vec2 uv) {
  vec2 px = 1.0 / u_res;
  vec2 u = uv + vec2(sin(uv.y * 40.0 + u_time * 3.0) * 0.35 * px.x, 0.0);
  vec4 c = src(u);
  c.r = src(u + vec2(1.5 * px.x, 0.0)).r;
  c.b = src(u - vec2(1.5 * px.x, 0.0)).b;
  float luma = dot(c.rgb, vec3(0.299, 0.587, 0.114));
  c.rgb = mix(c.rgb, vec3(luma), 0.2);
  // a brighter band rolls down the screen
  float band = abs(uv.y - fract(u_time * 0.07));
  c.rgb *= 1.0 + 0.07 * (1.0 - smoothstep(0.0, 0.05, band));
  c.rgb += (hash(floor(uv * u_res) + floor(u_time * 24.0)) - 0.5) * 0.06;
  return c;
}`},

	"dither": {Frag: `
float bayer2(vec2 a) { a = floor(a); return fract(a.x / 2.0 + a.y * a.y * 0.75); }
float bayer4(vec2 a) { return bayer2(0.5 * a) * 0.25 + bayer2(a); }
vec4 effect(vec2 uv) {
  vec4 c = src(uv);
  vec3 rgb = c.rgb + (1.0 - c.a) * BG; // the window clear behind the root is transparent in src
  // dark backgrounds go solid black and dim text solid white, so only
  // midtones like the selection dither
  float luma = smoothstep(0.14, 0.55, dot(rgb, vec3(0.299, 0.587, 0.114)));
  float on = step(bayer4(uv * u_res) + 0.03, luma);
  return vec4(vec3(on), 1.0);
}`},

	"glow": {Frag: `
vec4 effect(vec2 uv) {
  vec2 px = 1.0 / u_res;
  vec4 c = src(uv);
  vec3 sum = vec3(0.0);
  for (int i = -3; i <= 3; i++) {
    for (int j = -3; j <= 3; j++) {
      sum += max(src(uv + vec2(float(i), float(j)) * px * 1.5).rgb - 0.35, 0.0); // only text and bright fills bloom
    }
  }
  c.rgb += sum / 49.0 * 1.6;
  return c;
}`},

	"amber": {Frag: phosphor("vec3(1.0, 0.69, 0.0)")},
	"green": {Frag: phosphor("vec3(0.2, 1.0, 0.4)")},

	"film": {Animate: true, Frag: hash + `
vec4 effect(vec2 uv) {
  vec4 c = src(uv);
  c.rgb = mix(c.rgb, c.rgb * vec3(1.06, 1.0, 0.9), 0.7);
  c.rgb += (hash(uv * u_res + fract(u_time) * 97.0) - 0.5) * 0.07;
  vec2 d = uv * 2.0 - 1.0;
  c.rgb *= 1.0 - 0.25 * dot(d, d);
  return c;
}`},
}

func phosphor(color string) string {
	return `
const vec3 W = vec3(0.299, 0.587, 0.114);
float lum(vec2 uv) {
  vec4 c = src(uv);
  return dot(c.rgb + (1.0 - c.a) * BG, W); // the window clear behind the root is transparent in src
}
vec4 effect(vec2 uv) {
  vec2 px = 1.0 / u_res;
  float l = lum(uv);
  float halo = (lum(uv + vec2(1.5 * px.x, 0.0)) + lum(uv - vec2(1.5 * px.x, 0.0)) +
                lum(uv + vec2(0.0, 1.5 * px.y)) + lum(uv - vec2(0.0, 1.5 * px.y))) * 0.25;
  l = smoothstep(0.08, 0.85, l) + halo * 0.25;
  l *= 0.85 + 0.15 * sin(uv.y * u_res.y * 3.14159);
  return vec4(` + color + ` * l, 1.0);
}`
}

const hash = `
float hash(vec2 p) { return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453); }`

const glitch = `
float gHash(float n) { return fract(sin(n) * 43758.5453); }
vec4 effect(vec2 uv) {
  float g = u_glitch;
  vec2 px = 1.0 / u_res;
  float t = floor(u_time * 30.0);
  float line = floor(uv.y * 24.0);
  float torn = step(1.0 - g * 0.6, gHash(line * 7.13 + t));
  uv.x += torn * (gHash(line + t * 3.1) - 0.5) * 24.0 * px.x * g;
  vec4 c = look(uv);
  c.r = mix(c.r, look(uv + vec2(3.0 * px.x * g, 0.0)).r, g);
  c.b = mix(c.b, look(uv - vec2(3.0 * px.x * g, 0.0)).b, g);
  c.rgb *= 1.0 + 0.15 * g * sin(u_time * 90.0);
  return c;
}`

func rootFX(name string, bg [3]float64, g float64) caution.FX {
	fx := effects[name]
	if fx.Frag != "" {
		fx.Frag = fmt.Sprintf("const vec3 BG = vec3(%.4f, %.4f, %.4f);\n", bg[0], bg[1], bg[2]) + fx.Frag
	}
	if g <= 0 {
		return fx
	}
	look := "vec4 look(vec2 uv) { return src(uv); }"
	if fx.Frag != "" {
		look = strings.Replace(fx.Frag, "vec4 effect(vec2 uv)", "vec4 look(vec2 uv)", 1)
	}
	return caution.FX{Frag: look + glitch, Animate: true, Uniforms: map[string]float64{"u_glitch": g}}
}

const glitchFor = 700 * time.Millisecond

var shimmer = caution.FX{Animate: true, Frag: `
vec4 effect(vec2 uv) {
  vec4 c = src(uv);
  float x = uv.x * u_res.x;
  float at = fract(u_time / 3.0) * (u_res.x + 300.0) - 150.0;
  float band = exp(-pow((x - at) / 110.0, 2.0));
  vec3 hue = 0.5 + 0.5 * cos(6.2832 * (x / 600.0 - u_time * 0.2 + vec3(0.0, 0.33, 0.67)));
  c.rgb = mix(c.rgb, hue * c.a, band * 0.85);
  return c;
}`}

func backdrop(theme map[string]string) caution.FX {
	bg, accent := hexRGB(theme["panelAlt"]), hexRGB(theme["accent"])
	return caution.FX{Animate: true, Frag: `
vec4 effect(vec2 uv) {
  vec2 p = uv * vec2(1.0, u_res.y / u_res.x) * 3.0;
  float t = u_time * 0.05;
  float v = sin(p.x * 1.3 + t * 3.0 + sin(p.y * 1.7 + t * 2.0)) + sin(p.y * 2.1 - t * 2.5 + sin(p.x * 1.1 - t));
  float glow = smoothstep(0.1, 1.0, 0.5 + 0.25 * v) * 0.4 * (1.0 - 0.5 * uv.y);
  return vec4(mix(vec3(u_bgR, u_bgG, u_bgB), vec3(u_acR, u_acG, u_acB), glow), 1.0);
}`, Uniforms: map[string]float64{
		"u_bgR": bg[0], "u_bgG": bg[1], "u_bgB": bg[2],
		"u_acR": accent[0], "u_acG": accent[1], "u_acB": accent[2],
	}}
}

func hexRGB(hex string) [3]float64 {
	var r, g, b uint8
	fmt.Sscanf(strings.TrimPrefix(hex, "#"), "%02x%02x%02x", &r, &g, &b)
	return [3]float64{float64(r) / 255, float64(g) / 255, float64(b) / 255}
}

func effectNames() []string { return slices.Sorted(maps.Keys(effects)) }

var extras = []string{"ping", "shimmer", "sidebar"}

type fxSettings struct {
	effect                 string
	ping, shimmer, sidebar bool
}

func (a *app) fx() fxSettings {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	return fxSettings{effect: s.Effect, ping: s.FXPing, shimmer: s.FXShimmer, sidebar: s.FXSidebar}
}

func (a *app) setEffect(name string) error {
	if name == "off" {
		name = ""
	}
	if _, ok := effects[name]; !ok && name != "" {
		return fmt.Errorf("no effect called %s; there's %s", name, strings.Join(effectNames(), ", "))
	}
	if err := a.updateSettings(func(s *settings) { s.Effect = name }); err != nil {
		return err
	}
	a.notify()
	return nil
}

func (a *app) setExtra(name string, on bool) error {
	err := a.updateSettings(func(s *settings) {
		switch name {
		case "ping":
			s.FXPing = on
		case "shimmer":
			s.FXShimmer = on
		case "sidebar":
			s.FXSidebar = on
		}
	})
	if err != nil {
		return err
	}
	a.notify()
	return nil
}

func (a *app) ping() {
	if !a.fx().ping {
		return
	}
	a.mu.Lock()
	screens := slices.Collect(maps.Keys(a.screens))
	a.mu.Unlock()
	for _, sc := range screens {
		go sc.s.Update(sc.glitch)
	}
}
