export const skyVertexShader = /* glsl */ `
varying vec3 vDirection;

void main() {
  vec4 world = modelMatrix * vec4(position, 1.0);
  vDirection = world.xyz;
  gl_Position = projectionMatrix * viewMatrix * world;
}
`

export const skyFragmentShader = /* glsl */ `
uniform float uTime;
uniform vec3 uGalaxyFar;
uniform vec3 uGalaxyTangent;
uniform vec3 uGalaxyPole;
varying vec3 vDirection;

float hash31(vec3 p) {
  p = fract(p * 0.1031);
  p += dot(p, p.yzx + 33.33);
  return fract((p.x + p.y) * p.z);
}

void main() {
  vec3 rd = normalize(vDirection);
  vec3 color = vec3(0.004, 0.005, 0.01);

  // 左右走向：中间宽，向两边按高斯慢慢收窄。
  float delta = atan(dot(rd, uGalaxyTangent), dot(rd, uGalaxyFar));
  float taper = exp(-delta * delta * 3.2);
  float alongFade = exp(-delta * delta * 2.6);
  float lane = dot(rd, uGalaxyPole) - 0.012 * sin(delta * 2.4);
  float sigma = 0.022 + 0.145 * taper;
  float galaxy = exp(-lane * lane / (sigma * sigma));

  vec3 g = rd;
  for (int layer = 0; layer < 4; layer++) {
    float scale = 20.0 + float(layer) * 14.0;
    vec3 id = floor(g * scale);
    vec3 cell = fract(g * scale) - 0.5;
    float h = hash31(id + float(layer) * 19.0);
    float gate = smoothstep(0.92, 0.22, h) * smoothstep(0.06, 0.36, galaxy) * smoothstep(0.08, 0.62, alongFade);
    float size = mix(0.1, 0.24, fract(h * 13.0)) * mix(1.0, 0.7, float(layer) / 3.0);
    vec3 jitter = vec3(hash31(id + 1.7), hash31(id + 4.1), hash31(id + 8.3)) - 0.5;
    float star = smoothstep(size, 0.0, length(cell - jitter * 0.6));
    float pulse = 0.5 + 0.5 * sin(uTime * (0.5 + h * 1.5) + h * 28.0);
    float twinkle = 0.45 + 0.55 * pulse * pulse;
    float brightness = mix(0.4, 1.0, fract(h * 17.0));
    vec3 tint = mix(vec3(0.75, 0.84, 1.0), vec3(1.0, 0.95, 0.84), fract(h * 9.0));
    color += tint * star * gate * twinkle * brightness * alongFade;
    g = normalize(g.yzx + g.zxy * 0.13);
  }

  float spineSigma = 0.022 + 0.04 * taper;
  float spine = exp(-lane * lane / (spineSigma * spineSigma));
  vec3 s = rd;
  for (int layer = 0; layer < 3; layer++) {
    float scale = 46.0 + float(layer) * 20.0;
    vec3 id = floor(s * scale);
    vec3 cell = fract(s * scale) - 0.5;
    float h = hash31(id + float(layer) * 23.0);
    float gate = smoothstep(0.88, 0.2, h) * smoothstep(0.04, 0.4, spine) * smoothstep(0.06, 0.55, alongFade);
    float size = mix(0.12, 0.26, fract(h * 13.0));
    vec3 jitter = vec3(hash31(id + 2.2), hash31(id + 5.4), hash31(id + 7.8)) - 0.5;
    float star = smoothstep(size, 0.0, length(cell - jitter * 0.45));
    float pulse = 0.5 + 0.5 * sin(uTime * (0.7 + h * 1.8) + h * 20.0);
    float twinkle = 0.62 + 0.38 * pulse * pulse;
    vec3 tint = mix(vec3(0.82, 0.88, 1.0), vec3(1.0, 0.96, 0.9), fract(h * 9.0));
    color += tint * star * gate * twinkle * (0.28 + 0.55 * spine) * (0.45 + 0.55 * alongFade);
    s = normalize(s.yzx + s.zxy * 0.11);
  }

  vec3 n = rd;
  for (int layer = 0; layer < 3; layer++) {
    float scale = 18.0 + float(layer) * 14.0;
    vec3 id = floor(n * scale);
    vec3 cell = fract(n * scale) - 0.5;
    float h = hash31(id + float(layer) * 7.0);
    float size = mix(0.05, 0.15, fract(h * 13.0));
    vec3 jitter = vec3(hash31(id + 1.2), hash31(id + 3.4), hash31(id + 5.6)) - 0.5;
    float star = smoothstep(size, 0.0, length(cell - jitter * 0.45)) * step(0.88, h);
    float pulse = 0.5 + 0.5 * sin(uTime * (0.8 + h * 2.4) + h * 40.0);
    float twinkle = 0.12 + 0.88 * pulse * pulse;
    float brightness = mix(0.22, 1.05, fract(h * 17.0));
    vec3 tint = mix(vec3(0.75, 0.84, 1.0), vec3(1.0, 0.96, 0.88), fract(h * 9.0));
    color += tint * star * twinkle * brightness;
    n = normalize(n.yzx + n.zxy * 0.17);
  }
  gl_FragColor = vec4(color, 1.0);
}
`

export const hubVertexShader = /* glsl */ `
varying vec3 vWorld;
varying vec3 vNormal;

void main() {
  vec4 world = modelMatrix * vec4(position, 1.0);
  vWorld = world.xyz;
  vNormal = normalize(mat3(modelMatrix) * normal);
  gl_Position = projectionMatrix * viewMatrix * world;
}
`

export const hubFragmentShader = /* glsl */ `
uniform float uTime;
uniform vec3 uLightDir;
varying vec3 vWorld;
varying vec3 vNormal;

// 月球灰白：深灰 / 中灰 / 浅灰，不要品牌紫。
const vec3 ROCK_DARK = vec3(0.14, 0.14, 0.15);
const vec3 ROCK_MID = vec3(0.42, 0.41, 0.40);
const vec3 ROCK_LIT = vec3(0.72, 0.71, 0.68);
const vec3 DUST = vec3(0.58, 0.56, 0.53);

float hash13(vec3 p) {
  p = fract(p * 0.1031);
  p += dot(p, p.zyx + 31.32);
  return fract((p.x + p.y) * p.z);
}

float valueNoise(vec3 p) {
  vec3 i = floor(p);
  vec3 f = fract(p);
  f = f * f * (3.0 - 2.0 * f);
  float n000 = hash13(i);
  float n100 = hash13(i + vec3(1.0, 0.0, 0.0));
  float n010 = hash13(i + vec3(0.0, 1.0, 0.0));
  float n110 = hash13(i + vec3(1.0, 1.0, 0.0));
  float n001 = hash13(i + vec3(0.0, 0.0, 1.0));
  float n101 = hash13(i + vec3(1.0, 0.0, 1.0));
  float n011 = hash13(i + vec3(0.0, 1.0, 1.0));
  float n111 = hash13(i + vec3(1.0, 1.0, 1.0));
  float nx00 = mix(n000, n100, f.x);
  float nx10 = mix(n010, n110, f.x);
  float nx01 = mix(n001, n101, f.x);
  float nx11 = mix(n011, n111, f.x);
  return mix(mix(nx00, nx10, f.y), mix(nx01, nx11, f.y), f.z);
}

float fbm(vec3 p) {
  float value = 0.0;
  float weight = 0.5;
  for (int octave = 0; octave < 5; octave++) {
    value += weight * valueNoise(p);
    p = p * 2.15 + vec3(1.7, 9.2, 3.4);
    weight *= 0.5;
  }
  return value;
}

// 陨石坑：在球面上挖出圆坑。
float crater(vec3 p, vec3 center, float radius) {
  float d = length(p - center);
  float rim = smoothstep(radius * 0.55, radius, d) * smoothstep(radius * 1.35, radius, d);
  float bowl = 1.0 - smoothstep(0.0, radius * 0.7, d);
  return rim * 0.55 - bowl * 0.7;
}

void main() {
  vec3 N = normalize(vNormal);
  vec3 P = normalize(vWorld);
  vec3 L = normalize(uLightDir);

  // 多层 FBM：灰白/深灰岩石 + 陨石坑。
  float macro = fbm(P * 2.4);
  float detail = fbm(P * 8.0 + 17.0);
  float pits = 0.0;
  pits += crater(P, normalize(vec3(0.62, 0.28, 0.45)), 0.22);
  pits += crater(P, normalize(vec3(-0.55, 0.4, 0.3)), 0.16);
  pits += crater(P, normalize(vec3(0.1, -0.72, 0.35)), 0.2);
  pits += crater(P, normalize(vec3(-0.25, -0.15, -0.8)), 0.14);
  pits += crater(P, normalize(vec3(0.7, -0.35, -0.2)), 0.12);
  float height = macro * 0.65 + detail * 0.35 + pits;

  // 用噪声梯度扰动法线，突出凹凸。
  float e = 0.02;
  float hx = fbm((P + vec3(e, 0.0, 0.0)) * 2.4) - fbm((P - vec3(e, 0.0, 0.0)) * 2.4);
  float hy = fbm((P + vec3(0.0, e, 0.0)) * 2.4) - fbm((P - vec3(0.0, e, 0.0)) * 2.4);
  float hz = fbm((P + vec3(0.0, 0.0, e)) * 2.4) - fbm((P - vec3(0.0, 0.0, e)) * 2.4);
  vec3 bump = normalize(N + vec3(hx, hy, hz) * 0.55 + P * pits * 0.35);

  // 侧向太阳光：明暗交界线。
  float ndotl = dot(bump, L);
  float lit = smoothstep(-0.15, 0.55, ndotl);
  float terminator = smoothstep(-0.05, 0.2, ndotl);

  vec3 albedo = mix(ROCK_DARK, ROCK_MID, clamp(height, 0.0, 1.0));
  albedo = mix(albedo, ROCK_LIT, clamp(detail * 0.45 + pits * 0.2, 0.0, 1.0));
  albedo = mix(albedo, DUST, 0.12);

  vec3 ambient = albedo * 0.18;
  vec3 diffuse = albedo * lit * 1.05;
  // 夜侧极弱灰光，避免全黑，仍保持月球感。
  vec3 night = ROCK_DARK * (1.0 - terminator) * 0.35;
  float rim = pow(1.0 - abs(dot(bump, normalize(cameraPosition - vWorld))), 3.0);
  vec3 color = ambient + diffuse + night + ROCK_LIT * rim * 0.08 * terminator;
  color = color / (1.0 + color * 0.15);
  gl_FragColor = vec4(color, 1.0);
}
`

export const ringVertexShader = /* glsl */ `
varying vec2 vUv;

void main() {
  vUv = uv;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}
`

export const ringFragmentShader = /* glsl */ `
uniform float uGain;
varying vec2 vUv;

void main() {
  // RingGeometry：uv.y 从内径到外径，收成极细半透明环带。
  float radial = abs(vUv.y - 0.5) * 2.0;
  float band = smoothstep(1.0, 0.2, radial);
  float edge = pow(band, 1.8);
  vec3 color = vec3(0.55, 0.42, 0.95) * edge * uGain;
  gl_FragColor = vec4(color, edge * 0.65);
}
`

export const nodeVertexShader = /* glsl */ `
varying vec3 vNormal;
varying vec3 vWorld;
varying vec3 vLocal;

void main() {
  vec4 world = modelMatrix * vec4(position, 1.0);
  vWorld = world.xyz;
  vLocal = position;
  vNormal = normalize(mat3(modelMatrix) * normal);
  gl_Position = projectionMatrix * viewMatrix * world;
}
`

export const nodeFragmentShader = /* glsl */ `
uniform vec3 uColor;
uniform vec3 uLightDir;
uniform float uTime;
uniform float uPhase;
uniform float uFreq;
varying vec3 vNormal;
varying vec3 vWorld;
varying vec3 vLocal;

float hash13(vec3 p) {
  p = fract(p * 0.1031);
  p += dot(p, p.zyx + 31.32);
  return fract((p.x + p.y) * p.z);
}

float valueNoise(vec3 p) {
  vec3 i = floor(p);
  vec3 f = fract(p);
  f = f * f * (3.0 - 2.0 * f);
  return mix(
    mix(mix(hash13(i), hash13(i + vec3(1.0, 0.0, 0.0)), f.x),
        mix(hash13(i + vec3(0.0, 1.0, 0.0)), hash13(i + vec3(1.0, 1.0, 0.0)), f.x), f.y),
    mix(mix(hash13(i + vec3(0.0, 0.0, 1.0)), hash13(i + vec3(1.0, 0.0, 1.0)), f.x),
        mix(hash13(i + vec3(0.0, 1.0, 1.0)), hash13(i + vec3(1.0, 1.0, 1.0)), f.x), f.y),
    f.z
  );
}

void main() {
  vec3 N = normalize(vNormal);
  vec3 L = normalize(uLightDir);
  float rock = valueNoise(vLocal * 14.0);
  float grit = valueNoise(vLocal * 36.0 + 4.0);
  // 品牌色压进岩石灰，避免过曝灯泡感。
  vec3 rockTint = mix(uColor * 0.35, uColor * 0.7, rock);
  vec3 albedo = mix(rockTint * 0.55, rockTint, grit);
  float ndotl = max(dot(N, L), 0.0);
  float lit = 0.22 + 0.78 * ndotl;
  // 每个节点独立相位/频率，亮度在 0.5–1.0 呼吸。
  float breath = 0.5 + 0.5 * (0.5 + 0.5 * sin(uTime * uFreq + uPhase));
  vec3 color = albedo * lit * breath;
  color += uColor * pow(1.0 - abs(dot(N, normalize(cameraPosition - vWorld))), 3.0) * 0.12 * breath;
  color = color / (1.0 + color * 0.25);
  gl_FragColor = vec4(color, 1.0);
}
`

export const filamentVertexShader = /* glsl */ `
attribute float along;
varying float vAlong;

void main() {
  vAlong = along;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}
`

export const filamentFragmentShader = /* glsl */ `
uniform float uTime;
uniform float uGain;
uniform vec3 uHubColor;
uniform vec3 uNodeColor;
varying float vAlong;

void main() {
  // 光纤：#8B5CF6 平滑过渡到节点色，透明度约 0.4–0.6，亮度低于月球。
  float ramp = smoothstep(0.0, 1.0, vAlong);
  vec3 color = mix(uHubColor, uNodeColor, ramp);
  float pulse = 0.92 + 0.08 * sin(uTime * 1.1 - vAlong * 6.0);
  float alpha = mix(0.42, 0.58, ramp) * pulse;
  gl_FragColor = vec4(color * uGain, alpha);
}
`

export const particleVertexShader = /* glsl */ `
attribute float pointSize;
attribute vec3 pointColor;
varying vec3 vColor;

void main() {
  vColor = pointColor;
  vec4 view = modelViewMatrix * vec4(position, 1.0);
  // 极小光点，像流星划过。
  gl_PointSize = pointSize * (5.5 / max(-view.z, 0.35));
  gl_Position = projectionMatrix * view;
}
`

export const particleFragmentShader = /* glsl */ `
varying vec3 vColor;

void main() {
  vec2 uv = gl_PointCoord - vec2(0.5);
  float dist = length(uv);
  if (dist > 0.5) discard;
  float alpha = smoothstep(0.5, 0.0, dist);
  float core = pow(alpha, 2.6);
  // 流星头更亮，边缘仍收得很小。
  gl_FragColor = vec4(vColor * (alpha * 0.75 + core * 1.45), 1.0);
}
`

export const starVertexShader = /* glsl */ `
attribute float pointSize;
attribute float brightness;
uniform float uTime;
varying float vBright;

void main() {
  float twinkle = 0.2 + 0.8 * sin(uTime * (1.1 + brightness * 4.0) + position.x * 0.17 + position.z * 0.13);
  vBright = brightness * (0.35 + 0.65 * twinkle * twinkle);
  vec4 view = modelViewMatrix * vec4(position, 1.0);
  gl_PointSize = pointSize * (104.0 / max(-view.z, 1.0));
  gl_Position = projectionMatrix * view;
}
`

export const starFragmentShader = /* glsl */ `
varying float vBright;

void main() {
  vec2 uv = gl_PointCoord - vec2(0.5);
  float dist = length(uv);
  if (dist > 0.5) discard;
  float alpha = smoothstep(0.5, 0.0, dist);
  gl_FragColor = vec4(vec3(0.86, 0.92, 1.0) * vBright * alpha, 1.0);
}
`

export const depthVertexShader = /* glsl */ `
void main() {
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}
`

export const depthFragmentShader = /* glsl */ `
void main() {
  gl_FragColor = vec4(0.0, 0.0, 0.0, 1.0);
}
`

export const passVertexShader = /* glsl */ `
varying vec2 vUv;

void main() {
  vUv = uv;
  gl_Position = vec4(position.xy, 0.0, 1.0);
}
`

export const brightFragmentShader = /* glsl */ `
uniform sampler2D uTexture;
uniform float uThreshold;
varying vec2 vUv;

void main() {
  vec3 color = texture(uTexture, vUv).rgb;
  float luma = dot(color, vec3(0.2126, 0.7152, 0.0722));
  float keep = smoothstep(uThreshold, uThreshold + 0.55, luma);
  gl_FragColor = vec4(color * keep, 1.0);
}
`

export const blurFragmentShader = /* glsl */ `
uniform sampler2D uTexture;
uniform vec2 uDirection;
varying vec2 vUv;

void main() {
  vec3 sum = texture(uTexture, vUv).rgb * 0.227027;
  vec2 step1 = uDirection * 1.2;
  vec2 step2 = uDirection * 2.6;
  vec2 step3 = uDirection * 4.2;
  sum += texture(uTexture, vUv + step1).rgb * 0.1945946;
  sum += texture(uTexture, vUv - step1).rgb * 0.1945946;
  sum += texture(uTexture, vUv + step2).rgb * 0.1216216;
  sum += texture(uTexture, vUv - step2).rgb * 0.1216216;
  sum += texture(uTexture, vUv + step3).rgb * 0.054054;
  sum += texture(uTexture, vUv - step3).rgb * 0.054054;
  gl_FragColor = vec4(sum, 1.0);
}
`

export const compositeFragmentShader = /* glsl */ `
uniform sampler2D uScene;
uniform sampler2D uBloom;
uniform float uStrength;
varying vec2 vUv;

void main() {
  vec3 sceneColor = texture(uScene, vUv).rgb;
  vec3 bloom = texture(uBloom, vUv).rgb;
  // 微弱辉光，不能把 1px 线糊成粗面条。
  vec3 color = sceneColor + bloom * uStrength;
  color = color / (1.0 + color * 0.12);
  gl_FragColor = vec4(color, 1.0);
}
`
