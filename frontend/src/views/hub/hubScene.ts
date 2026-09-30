import * as THREE from 'three'
import {
  GM,
  HUB_NODES,
  HUB_PURPLE,
  HUB_RADIUS,
  deflectionSag,
  hexToRgb,
  nodePosition,
} from './hubPhysics'
import {
  blurFragmentShader,
  brightFragmentShader,
  compositeFragmentShader,
  depthFragmentShader,
  depthVertexShader,
  filamentFragmentShader,
  filamentVertexShader,
  hubFragmentShader,
  hubVertexShader,
  nodeFragmentShader,
  nodeVertexShader,
  particleFragmentShader,
  particleVertexShader,
  passVertexShader,
  ringFragmentShader,
  ringVertexShader,
  skyFragmentShader,
  skyVertexShader,
  starFragmentShader,
  starVertexShader,
} from './hubShader'

const LINE_POINTS = 64
const STREAMS = 1
const TRAIL = 5
const STAR_COUNT = 280
// 再放大一倍，仍小于中心月球（HUB_RADIUS ≈ 0.92）。
const NODE_RADIUS = 0.3

/**
 * 鼠标移动驱动的球面相机，方位角和极角的更新方式与 OrbitControls 相同。
 * 当前安装的 three 包没有附带 examples/jsm/controls/OrbitControls.js。
 */
export interface HubNodeLabel {
  name: string
  color: string
  x: number
  y: number
  visible: boolean
}

export interface HubView {
  resize(width: number, height: number): void
  setPointer(x: number, y: number): void
  addDistance(delta: number): void
  setFlow(flow: number): void
  getLabels(): HubNodeLabel[]
  render(time: number): void
  dispose(): void
}

function wrap01(value: number): number {
  const fraction = value % 1
  return fraction < 0 ? fraction + 1 : fraction
}

function filamentPoint(
  a: THREE.Vector3,
  c: THREE.Vector3,
  side: THREE.Vector3,
  t: number,
  target: THREE.Vector3,
): THREE.Vector3 {
  // 靠近中心收紧，外半段才弯，像引力线而不是闪电。
  const p1x = a.x + (c.x - a.x) * 0.18
  const p1y = a.y + (c.y - a.y) * 0.18
  const p1z = a.z + (c.z - a.z) * 0.18
  const p2x = a.x + (c.x - a.x) * 0.72 + side.x
  const p2y = a.y + (c.y - a.y) * 0.72 + side.y
  const p2z = a.z + (c.z - a.z) * 0.72 + side.z
  const u = 1 - t
  const uu = u * u
  const tt = t * t
  return target.set(
    uu * u * a.x + 3 * uu * t * p1x + 3 * u * tt * p2x + tt * t * c.x,
    uu * u * a.y + 3 * uu * t * p1y + 3 * u * tt * p2y + tt * t * c.y,
    uu * u * a.z + 3 * uu * t * p1z + 3 * u * tt * p2z + tt * t * c.z,
  )
}

function createFilament(): { geometry: THREE.BufferGeometry; position: THREE.BufferAttribute; along: THREE.BufferAttribute } {
  const positions = new Float32Array(LINE_POINTS * 3)
  const along = new Float32Array(LINE_POINTS)
  const geometry = new THREE.BufferGeometry()
  const position = new THREE.BufferAttribute(positions, 3)
  const alongAttribute = new THREE.BufferAttribute(along, 1)
  geometry.setAttribute('position', position)
  geometry.setAttribute('along', alongAttribute)
  return { geometry, position, along: alongAttribute }
}

/** Icosahedron + 顶点噪声，做成不规则小行星。 */
function createAsteroidGeometry(radius: number, seed: number): THREE.BufferGeometry {
  const geometry = new THREE.IcosahedronGeometry(radius, 2)
  const position = geometry.getAttribute('position')
  for (let index = 0; index < position.count; index += 1) {
    const x = position.getX(index)
    const y = position.getY(index)
    const z = position.getZ(index)
    const length = Math.hypot(x, y, z) || 1
    const nx = x / length
    const ny = y / length
    const nz = z / length
    // 伪随机扰动：每个节点 seed 不同，形状不重复。
    const n1 = Math.sin(nx * 9.1 + ny * 5.7 + nz * 3.3 + seed * 12.7) * 0.5 + 0.5
    const n2 = Math.sin(nx * 17.3 - ny * 11.2 + nz * 8.4 + seed * 4.1) * 0.5 + 0.5
    const dent = (n1 * 0.65 + n2 * 0.35) * 0.28 - 0.08
    const scale = 1 + dent
    position.setXYZ(index, nx * radius * scale, ny * radius * scale, nz * radius * scale)
  }
  position.needsUpdate = true
  geometry.computeVertexNormals()
  return geometry
}

export function createHubView(canvas: HTMLCanvasElement): HubView | null {
  let renderer: THREE.WebGLRenderer
  try {
    renderer = new THREE.WebGLRenderer({
      canvas,
      antialias: true,
      alpha: false,
      powerPreference: 'high-performance',
    })
  } catch {
    return null
  }
  if (!renderer.getContext()) {
    renderer.dispose()
    return null
  }

  renderer.outputColorSpace = THREE.LinearSRGBColorSpace
  renderer.toneMapping = THREE.NoToneMapping
  renderer.setClearColor(0x000000, 1)

  const scene = new THREE.Scene()
  const camera = new THREE.PerspectiveCamera(42, 1, 0.1, 200)
  const time = { value: 0 }
  const disposables: Array<{ dispose(): void }> = []
  const track = <T extends { dispose(): void }>(item: T): T => {
    disposables.push(item)
    return item
  }

  const galaxyFar = new THREE.Vector3(0, 0, -1)
  const galaxyTangent = new THREE.Vector3(1, 0, 0)
  const galaxyPole = new THREE.Vector3(0, 1, 0)
  const skyMaterial = track(new THREE.ShaderMaterial({
    uniforms: {
      uTime: time,
      uGalaxyFar: { value: galaxyFar },
      uGalaxyTangent: { value: galaxyTangent },
      uGalaxyPole: { value: galaxyPole },
    },
    vertexShader: skyVertexShader,
    fragmentShader: skyFragmentShader,
    side: THREE.BackSide,
    depthTest: false,
    depthWrite: false,
  }))
  const sky = new THREE.Mesh(track(new THREE.SphereGeometry(80, 32, 24)), skyMaterial)
  sky.renderOrder = -1
  sky.frustumCulled = false
  scene.add(sky)

  const starPositions = new Float32Array(STAR_COUNT * 3)
  const starSizes = new Float32Array(STAR_COUNT)
  const starBrightness = new Float32Array(STAR_COUNT)
  const golden = Math.PI * (3 - Math.sqrt(5))
  for (let index = 0; index < STAR_COUNT; index += 1) {
    const y = 1 - (index / (STAR_COUNT - 1)) * 2
    const ring = Math.sqrt(Math.max(0, 1 - y * y))
    const theta = golden * index
    const radius = 48 + (index % 5) * 1.2
    starPositions[index * 3] = Math.cos(theta) * ring * radius
    starPositions[index * 3 + 1] = y * radius
    starPositions[index * 3 + 2] = Math.sin(theta) * ring * radius
    starSizes[index] = 1.75 + (index % 5) * 0.58
    starBrightness[index] = 0.22 + (index % 8) * 0.08
  }
  const starGeometry = track(new THREE.BufferGeometry())
  starGeometry.setAttribute('position', new THREE.BufferAttribute(starPositions, 3))
  starGeometry.setAttribute('pointSize', new THREE.BufferAttribute(starSizes, 1))
  starGeometry.setAttribute('brightness', new THREE.BufferAttribute(starBrightness, 1))
  const starMaterial = track(new THREE.ShaderMaterial({
    uniforms: { uTime: time },
    vertexShader: starVertexShader,
    fragmentShader: starFragmentShader,
    transparent: true,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  }))
  const stars = new THREE.Points(starGeometry, starMaterial)
  stars.frustumCulled = false
  stars.renderOrder = 0
  scene.add(stars)

  // 月球后方的银河：左右走向，中间宽，向两边平滑变窄。
  const bandCount = 4800
  const coreCount = 4200
  const galaxyCount = bandCount + coreCount
  const galaxyPositions = new Float32Array(galaxyCount * 3)
  const galaxySizes = new Float32Array(galaxyCount)
  const galaxyBrightness = new Float32Array(galaxyCount)
  const restAzimuth = 0.32
  const restPolar = 0.95
  const restDistance = 9.2
  const restSin = Math.sin(restPolar)
  const camPos = new THREE.Vector3(
    restSin * Math.sin(restAzimuth) * restDistance,
    Math.cos(restPolar) * restDistance,
    restSin * Math.cos(restAzimuth) * restDistance,
  )
  const look = new THREE.Vector3(-0.45, 0.18, 0)
  const back = new THREE.Vector3().copy(camPos).sub(look).normalize()
  const right = new THREE.Vector3(0, 1, 0)
  right.cross(back).normalize()
  const far = new THREE.Vector3(-camPos.x, -camPos.y, -camPos.z).normalize()
  const tangent = new THREE.Vector3().copy(right)
  const ontoFar = tangent.dot(far)
  tangent.set(
    tangent.x - far.x * ontoFar,
    tangent.y - far.y * ontoFar,
    tangent.z - far.z * ontoFar,
  ).normalize()
  const pole = new THREE.Vector3().copy(far)
  pole.cross(tangent).normalize()
  galaxyFar.copy(far)
  galaxyTangent.copy(tangent)
  galaxyPole.copy(pole)
  const hashUnit = (n: number) => {
    const x = Math.sin(n * 127.1) * 43758.5453
    return x - Math.floor(x)
  }
  for (let index = 0; index < galaxyCount; index += 1) {
    const u = Math.max(hashUnit(index + 1.3), 1e-4)
    const v = hashUnit(index * 1.7 + 9.2)
    const roll = Math.max(hashUnit(index + 20.2), 1e-4)
    const spin = hashUnit(index + 21.7)
    let alongOffset = Math.sqrt(-2 * Math.log(roll)) * Math.cos(Math.PI * 2 * spin) * 0.34
    if (alongOffset > 1.05) alongOffset = 1.05
    if (alongOffset < -1.05) alongOffset = -1.05
    const taper = Math.exp(-alongOffset * alongOffset * 3.2)
    const onSpine = index >= bandCount
    const tight = onSpine && hashUnit(index + 4.4) < 0.34
    const laneSigma = !onSpine
      ? 0.03 + 0.13 * taper
      : tight
        ? 0.01 + 0.016 * taper
        : 0.034 + 0.055 * taper
    const center = 0.012 * Math.sin(alongOffset * 2.4)
    const lane = Math.sqrt(-2 * Math.log(u)) * Math.cos(Math.PI * 2 * v) * laneSigma + center
    const cosLane = Math.cos(lane)
    const sinAlong = Math.sin(alongOffset)
    const cosAlong = Math.cos(alongOffset)
    const sinLane = Math.sin(lane)
    const dirX = far.x * cosAlong * cosLane + tangent.x * sinAlong * cosLane + pole.x * sinLane
    const dirY = far.y * cosAlong * cosLane + tangent.y * sinAlong * cosLane + pole.y * sinLane
    const dirZ = far.z * cosAlong * cosLane + tangent.z * sinAlong * cosLane + pole.z * sinLane
    const radius = 26 + hashUnit(index + 3.3) * 20
    galaxyPositions[index * 3] = dirX * radius
    galaxyPositions[index * 3 + 1] = dirY * radius
    galaxyPositions[index * 3 + 2] = dirZ * radius
    galaxySizes[index] = onSpine
      ? 0.7 + hashUnit(index + 6.6) * 0.95
      : 0.55 + hashUnit(index + 6.6) * 1.45
    const dist = lane - center
    const fadeSigma = tight ? 0.022 + 0.02 * taper : 0.05 + 0.045 * taper
    const fade = Math.exp(-(dist * dist) / (fadeSigma * fadeSigma + 0.0004))
    const edgeFade = 0.55 + 0.45 * taper
    const spark = 0.55 + hashUnit(index + 8.8) * 0.45
    galaxyBrightness[index] = onSpine
      ? (0.2 + fade * (tight ? 1.15 : 0.7) * spark) * (0.68 + 0.32 * taper)
      : (0.16 + fade * 0.55 * spark) * edgeFade
  }
  const galaxyGeometry = track(new THREE.BufferGeometry())
  galaxyGeometry.setAttribute('position', new THREE.BufferAttribute(galaxyPositions, 3))
  galaxyGeometry.setAttribute('pointSize', new THREE.BufferAttribute(galaxySizes, 1))
  galaxyGeometry.setAttribute('brightness', new THREE.BufferAttribute(galaxyBrightness, 1))
  const galaxy = new THREE.Points(galaxyGeometry, starMaterial)
  galaxy.frustumCulled = false
  galaxy.renderOrder = 0
  scene.add(galaxy)

  // 侧向主光，模拟太阳，给月球和节点明暗交界线。
  const lightDir = new THREE.Vector3(0.72, 0.28, 0.55).normalize()
  const hubMaterial = track(new THREE.ShaderMaterial({
    uniforms: { uTime: time, uLightDir: { value: lightDir } },
    vertexShader: hubVertexShader,
    fragmentShader: hubFragmentShader,
  }))
  const hub = new THREE.Group()
  // 中心半径保持 HUB_RADIUS 不变，只换月球材质。
  const coreMesh = new THREE.Mesh(track(new THREE.SphereGeometry(HUB_RADIUS, 64, 48)), hubMaterial)
  hub.add(coreMesh)
  const depthMaterial = track(new THREE.ShaderMaterial({
    vertexShader: depthVertexShader,
    fragmentShader: depthFragmentShader,
  }))

  // 2–3 层极细引力环，像土星环，缓慢自转；亮度压低，不抢月球。
  const gravityRings: THREE.Mesh[] = []
  const ringSpecs = [
    { inner: HUB_RADIUS * 1.28, outer: HUB_RADIUS * 1.32, gain: 0.32, tilt: 0.35, speed: 0.08 },
    { inner: HUB_RADIUS * 1.48, outer: HUB_RADIUS * 1.515, gain: 0.22, tilt: -0.22, speed: -0.055 },
    { inner: HUB_RADIUS * 1.68, outer: HUB_RADIUS * 1.705, gain: 0.14, tilt: 0.12, speed: 0.035 },
  ]
  ringSpecs.forEach((spec) => {
    const material = track(new THREE.ShaderMaterial({
      uniforms: { uGain: { value: spec.gain } },
      vertexShader: ringVertexShader,
      fragmentShader: ringFragmentShader,
      transparent: true,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
      side: THREE.DoubleSide,
    }))
    const mesh = new THREE.Mesh(
      track(new THREE.RingGeometry(spec.inner, spec.outer, 96, 1)),
      material,
    )
    mesh.rotation.set(Math.PI / 2 + spec.tilt, 0, spec.tilt * 0.4)
    mesh.renderOrder = 1
    hub.add(mesh)
    gravityRings.push(mesh)
  })
  scene.add(hub)

  const hubColor = new THREE.Vector3(...hexToRgb(HUB_PURPLE))
  const filaments = HUB_NODES.map((node) => {
    const nodeColor = new THREE.Vector3(...hexToRgb(node.color))
    const makeMaterial = (gain: number) => track(new THREE.ShaderMaterial({
      uniforms: {
        uTime: time,
        uGain: { value: gain },
        uHubColor: { value: hubColor },
        uNodeColor: { value: nodeColor },
      },
      vertexShader: filamentVertexShader,
      fragmentShader: filamentFragmentShader,
      transparent: true,
      blending: THREE.NormalBlending,
      depthWrite: false,
    }))
    const filament = createFilament()
    track(filament.geometry)
    // 画面线用品牌色、半透明；bloom 只补一层很弱的辉光。
    const displayMaterial = makeMaterial(0.85)
    const bloomMaterial = makeMaterial(0.7)
    const line = new THREE.Line(filament.geometry, displayMaterial)
    line.frustumCulled = false
    line.renderOrder = 2
    scene.add(line)
    return { line, displayMaterial, bloomMaterial, position: filament.position, along: filament.along }
  })

  const nodes = HUB_NODES.map((node, index) => {
    const color = new THREE.Vector3(...hexToRgb(node.color))
    const group = new THREE.Group()
    // 独立相位/频率，错落呼吸，不要同步闪。
    const body = track(new THREE.ShaderMaterial({
      uniforms: {
        uColor: { value: color },
        uLightDir: { value: lightDir },
        uTime: time,
        uPhase: { value: index * 1.37 + 0.4 },
        uFreq: { value: 0.55 + index * 0.23 },
      },
      vertexShader: nodeVertexShader,
      fragmentShader: nodeFragmentShader,
    }))
    group.add(new THREE.Mesh(track(createAsteroidGeometry(NODE_RADIUS, index + 1)), body))
    scene.add(group)
    return group
  })

  // 每条丝线一条流星拖尾：小、快、暗。
  const particleCount = HUB_NODES.length * STREAMS * TRAIL
  const particlePositions = new Float32Array(particleCount * 3)
  const particleColors = new Float32Array(particleCount * 3)
  const particleSizes = new Float32Array(particleCount)
  const hubRgb = hexToRgb(HUB_PURPLE)
  const nodeRgbs = HUB_NODES.map((node) => hexToRgb(node.color))
  for (let index = 0; index < particleCount; index += 1) {
    const fade = 1 - (index % TRAIL) / TRAIL
    particleSizes[index] = 1.5 + 3.6 * fade * fade
  }
  const particleGeometry = track(new THREE.BufferGeometry())
  const particlePosition = new THREE.BufferAttribute(particlePositions, 3)
  const particleColor = new THREE.BufferAttribute(particleColors, 3)
  particleGeometry.setAttribute('position', particlePosition)
  particleGeometry.setAttribute('pointColor', particleColor)
  particleGeometry.setAttribute('pointSize', new THREE.BufferAttribute(particleSizes, 1))
  const particles = new THREE.Points(particleGeometry, track(new THREE.ShaderMaterial({
    uniforms: {},
    vertexShader: particleVertexShader,
    fragmentShader: particleFragmentShader,
    transparent: true,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })))
  particles.renderOrder = 3
  scene.add(particles)

  const makeTarget = (width: number, height: number, depth: boolean) => track(new THREE.WebGLRenderTarget(width, height, {
    minFilter: THREE.LinearFilter,
    magFilter: THREE.LinearFilter,
    depthBuffer: depth,
  }))
  const sceneTarget = makeTarget(1, 1, true)
  const lineTarget = makeTarget(1, 1, true)
  const bloomA = makeTarget(1, 1, false)
  const bloomB = makeTarget(1, 1, false)
  let bloomWidth = 1
  let bloomHeight = 1
  const postScene = new THREE.Scene()
  const postCamera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.01, 2)
  postCamera.position.set(0, 0, 1)
  postCamera.lookAt(0, 0, 0)
  const brightMaterial = track(new THREE.ShaderMaterial({
    uniforms: {
      uTexture: { value: lineTarget.texture },
      uThreshold: { value: 0.12 },
    },
    vertexShader: passVertexShader,
    fragmentShader: brightFragmentShader,
    depthTest: false,
    depthWrite: false,
  }))
  const blurDirection = new THREE.Vector2(1, 0)
  const blurMaterial = track(new THREE.ShaderMaterial({
    uniforms: {
      uTexture: { value: bloomA.texture },
      uDirection: { value: blurDirection },
    },
    vertexShader: passVertexShader,
    fragmentShader: blurFragmentShader,
    depthTest: false,
    depthWrite: false,
  }))
  const compositeMaterial = track(new THREE.ShaderMaterial({
    uniforms: {
      uScene: { value: sceneTarget.texture },
      uBloom: { value: bloomA.texture },
      // 只给细线一点辉光，强度刻意压低。
      uStrength: { value: 0.22 },
    },
    vertexShader: passVertexShader,
    fragmentShader: compositeFragmentShader,
    depthTest: false,
    depthWrite: false,
  }))
  const quad = new THREE.Mesh(track(new THREE.PlaneGeometry(2, 2)), brightMaterial)
  postScene.add(quad)

  const cursor = {
    a: new THREE.Vector3(),
    c: new THREE.Vector3(),
    point: new THREE.Vector3(),
    side: new THREE.Vector3(),
  }

  let viewWidth = 1
  let viewHeight = 1
  const projectPoint = new THREE.Vector3()
  const labelAnchors: HubNodeLabel[] = HUB_NODES.map((node) => ({
    name: node.name,
    color: node.color,
    x: 0,
    y: 0,
    visible: false,
  }))

  let flow = 1
  // 右栏画布更大后略拉近，恢复接近全屏时的月球体量。
  let distance = 9.2
  let azimuth = 0.32
  let polar = 0.95
  let targetAzimuth = 0.32
  let targetPolar = 0.95

  function writeLine(position: THREE.BufferAttribute, along: THREE.BufferAttribute) {
    for (let i = 0; i < LINE_POINTS; i += 1) {
      const t = i / (LINE_POINTS - 1)
      filamentPoint(cursor.a, cursor.c, cursor.side, t, cursor.point)
      position.setXYZ(i, cursor.point.x, cursor.point.y, cursor.point.z)
      along.setX(i, t)
    }
    position.needsUpdate = true
    along.needsUpdate = true
  }

  function setLineBloomSource(enabled: boolean) {
    sky.visible = !enabled
    stars.visible = !enabled
    galaxy.visible = !enabled
    particles.visible = !enabled
    for (let index = 0; index < gravityRings.length; index += 1) gravityRings[index].visible = !enabled
    for (let index = 0; index < nodes.length; index += 1) nodes[index].visible = !enabled
    coreMesh.material = enabled ? depthMaterial : hubMaterial
    for (let index = 0; index < filaments.length; index += 1) {
      filaments[index].line.material = enabled ? filaments[index].bloomMaterial : filaments[index].displayMaterial
    }
  }

  function placeCamera() {
    const sinPolar = Math.sin(polar)
    camera.position.set(
      sinPolar * Math.sin(azimuth) * distance,
      Math.cos(polar) * distance,
      sinPolar * Math.cos(azimuth) * distance,
    )
    // lookAt 偏左 → 月球偏右，不要往页面中缝挤。
    camera.lookAt(-0.45, 0.18, 0)
  }

  function toScreen(x: number, y: number, z: number): { x: number; y: number; visible: boolean } {
    projectPoint.set(x, y, z).project(camera)
    const visible = projectPoint.z > -1 && projectPoint.z < 1
      && projectPoint.x > -1.15 && projectPoint.x < 1.15
      && projectPoint.y > -1.15 && projectPoint.y < 1.15
    return {
      x: (projectPoint.x * 0.5 + 0.5) * viewWidth,
      y: (-projectPoint.y * 0.5 + 0.5) * viewHeight,
      visible,
    }
  }

  function placeLabelBelow(
    target: HubNodeLabel,
    x: number,
    y: number,
    z: number,
    radius: number,
    gap: number,
  ) {
    const center = toScreen(x, y, z)
    const rim = toScreen(x, y - radius, z)
    const offset = Math.max(12, Math.abs(rim.y - center.y) + gap)
    target.x = Math.min(viewWidth - 6, Math.max(6, center.x))
    target.y = Math.min(viewHeight - 6, Math.max(6, center.y + offset))
    target.visible = center.visible
  }

  return {
    resize(width: number, height: number) {
      const ratio = Math.min(window.devicePixelRatio || 1, 1.5)
      renderer.setPixelRatio(ratio)
      renderer.setSize(width, height, false)
      const pixelWidth = Math.max(1, Math.floor(width * ratio))
      const pixelHeight = Math.max(1, Math.floor(height * ratio))
      sceneTarget.setSize(pixelWidth, pixelHeight)
      lineTarget.setSize(pixelWidth, pixelHeight)
      bloomWidth = Math.max(1, Math.floor(pixelWidth / 2))
      bloomHeight = Math.max(1, Math.floor(pixelHeight / 2))
      bloomA.setSize(bloomWidth, bloomHeight)
      bloomB.setSize(bloomWidth, bloomHeight)
      camera.aspect = width / Math.max(height, 1)
      camera.updateProjectionMatrix()
      viewWidth = width
      viewHeight = height
    },
    setPointer(x: number, y: number) {
      targetAzimuth = 0.32 + x * 0.55
      targetPolar = Math.min(1.25, Math.max(0.6, 0.95 + y * 0.3))
    },
    addDistance(delta: number) {
      distance = Math.min(15, Math.max(7.5, distance + delta))
    },
    setFlow(next: number) {
      flow = next
    },
    getLabels() {
      return labelAnchors
    },
    render(now: number) {
      const seconds = now / 1000
      time.value = seconds
      azimuth += (targetAzimuth - azimuth) * 0.08
      polar += (targetPolar - polar) * 0.08
      placeCamera()

      // 中心几乎不动，只做极轻微呼吸；引力环缓慢自转。
      const pulse = 1 + 0.018 * Math.sin(seconds * 0.7)
      hub.scale.set(pulse, pulse, pulse)
      for (let index = 0; index < gravityRings.length; index += 1) {
        gravityRings[index].rotation.z = seconds * ringSpecs[index].speed
      }

      for (let index = 0; index < HUB_NODES.length; index += 1) {
        const [x, y, z] = nodePosition(index, seconds)
        nodes[index].position.set(x, y, z)
        const toCamera = Math.hypot(camera.position.x - x, camera.position.y - y, camera.position.z - z) || distance
        const compensate = Math.min(1.2, Math.max(0.82, Math.pow(toCamera / distance, 0.7)))
        nodes[index].scale.set(compensate, compensate, compensate)

        const radius = Math.hypot(x, y, z) || 1
        cursor.a.set((x / radius) * HUB_RADIUS, (y / radius) * HUB_RADIUS, (z / radius) * HUB_RADIUS)
        const sag = deflectionSag(GM, HUB_RADIUS, radius)
        cursor.side.set(-z, 0, x)
        if (cursor.side.length() < 1e-4) cursor.side.set(0, 0, 1)
        cursor.side.normalize().multiplyScalar(sag)
        // 丝线接到小行星表面，而不是穿进球心。
        const surface = Math.max(radius - NODE_RADIUS * compensate * 0.92, HUB_RADIUS + 0.05)
        cursor.c.set((x / radius) * surface, (y / radius) * surface, (z / radius) * surface)
        writeLine(filaments[index].position, filaments[index].along)
        placeLabelBelow(labelAnchors[index], x, y, z, NODE_RADIUS * compensate, 8)

        const direction = flow < 0 ? -1 : 1
        const [nr, ng, nb] = nodeRgbs[index]
        for (let stream = 0; stream < STREAMS; stream += 1) {
          const head = wrap01(seconds * 1.15 * direction + index * 0.17 + stream * 0.37)
          for (let step = 0; step < TRAIL; step += 1) {
            const t = wrap01(head - step * 0.007 * direction)
            filamentPoint(cursor.a, cursor.c, cursor.side, t, cursor.point)
            const slot = (index * STREAMS + stream) * TRAIL + step
            particlePosition.setXYZ(slot, cursor.point.x, cursor.point.y, cursor.point.z)
            const ramp = t * t * (3 - 2 * t)
            const fade = 1 - step / TRAIL
            const spark = fade * fade
            const bright = 0.45 + 1.25 * spark
            particleColor.setXYZ(
              slot,
              (hubRgb[0] + (nr - hubRgb[0]) * ramp) * bright + spark * 0.7,
              (hubRgb[1] + (ng - hubRgb[1]) * ramp) * bright + spark * 0.7,
              (hubRgb[2] + (nb - hubRgb[2]) * ramp) * bright + spark * 0.7,
            )
          }
        }
      }
      particlePosition.needsUpdate = true
      particleColor.needsUpdate = true

      renderer.setRenderTarget(sceneTarget)
      renderer.render(scene, camera)

      // Bloom 只抽丝线，不抽粒子/节点，避免再出现白色粗面条。
      setLineBloomSource(true)
      renderer.setRenderTarget(lineTarget)
      renderer.render(scene, camera)
      setLineBloomSource(false)

      quad.material = brightMaterial
      renderer.setRenderTarget(bloomA)
      renderer.render(postScene, postCamera)
      const texelX = 0.85 / bloomWidth
      const texelY = 0.85 / bloomHeight
      quad.material = blurMaterial
      blurMaterial.uniforms.uTexture.value = bloomA.texture
      blurDirection.set(texelX, 0)
      renderer.setRenderTarget(bloomB)
      renderer.render(postScene, postCamera)
      blurMaterial.uniforms.uTexture.value = bloomB.texture
      blurDirection.set(0, texelY)
      renderer.setRenderTarget(bloomA)
      renderer.render(postScene, postCamera)

      quad.material = compositeMaterial
      renderer.setRenderTarget(null)
      renderer.render(postScene, postCamera)
    },
    dispose() {
      disposables.forEach((item) => item.dispose())
      renderer.dispose()
    },
  }
}
