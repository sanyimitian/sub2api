/**
 * 中转枢纽是焦点。节点走椭圆，平均角速度由开普勒第三定律决定：
 *
 *   n² = G M / a³
 *   M = n t + M₀
 *   M = E − e sin E
 *   r = a (1 − e cos E)
 *
 * a 是半长轴。a 越小，n 越大，所以内圈转得更快。
 * 真近点角处的半径在近日点 a(1−e) 和远日点 a(1+e) 之间变化。
 *
 * 丝线按弱场偏折弯开，c = 1：
 *   δθ ≈ 2 G M (1/r_hub − 1/r_node)
 *   中点横向偏移 ≈ δθ · r_node / 2
 * VISUAL_BEND 只放大屏幕上的弯折，不改变公转。
 */

export const HUB_PURPLE = '#8B5CF6'
export const HUB_BLUE = '#3B82F6'
export const GM = 3.5
export const HUB_RADIUS = 0.92
export const VISUAL_BEND = 0.045

export interface KeplerOrbit {
  semiMajor: number
  eccentricity: number
  inclination: number
  ascendingNode: number
  periapsis: number
  meanAnomaly: number
}

export const HUB_NODES: ReadonlyArray<{ name: string; color: string; orbit: KeplerOrbit }> = [
  // 半长轴压到约 2.0–2.7，六个节点围成紧凑星系，四周留黑。
  { name: 'OpenAI', color: '#22c55e', orbit: { semiMajor: 2.05, eccentricity: 0.12, inclination: 0.14, ascendingNode: 0.1, periapsis: 0.4, meanAnomaly: 0.0 } },
  { name: 'Claude', color: '#f97316', orbit: { semiMajor: 2.2, eccentricity: 0.15, inclination: -0.28, ascendingNode: 1.2, periapsis: 0.8, meanAnomaly: 1.05 } },
  { name: 'Gemini', color: '#3b82f6', orbit: { semiMajor: 2.35, eccentricity: 0.1, inclination: 0.36, ascendingNode: 2.3, periapsis: 0.2, meanAnomaly: 2.1 } },
  { name: 'Grok', color: '#e4e4e7', orbit: { semiMajor: 2.5, eccentricity: 0.16, inclination: -0.18, ascendingNode: 3.4, periapsis: 1.1, meanAnomaly: 3.15 } },
  { name: 'Kimi', color: '#ec4899', orbit: { semiMajor: 2.65, eccentricity: 0.11, inclination: 0.24, ascendingNode: 4.5, periapsis: 0.6, meanAnomaly: 4.2 } },
  { name: 'Antigravity', color: '#a855f7', orbit: { semiMajor: 2.8, eccentricity: 0.14, inclination: -0.32, ascendingNode: 5.6, periapsis: 1.4, meanAnomaly: 5.25 } },
]

export function orbitalFrequency(semiMajor: number, gm = GM): number {
  return Math.sqrt(gm / (semiMajor * semiMajor * semiMajor))
}

function wrapPi(angle: number): number {
  const tau = Math.PI * 2
  let wrapped = (angle + Math.PI) % tau
  if (wrapped < 0) wrapped += tau
  return wrapped - Math.PI
}

/** 牛顿迭代解开普勒方程 M = E − e sin E。 */
export function eccentricAnomaly(meanAnomaly: number, eccentricity: number): number {
  const mean = wrapPi(meanAnomaly)
  let anomaly = mean
  for (let i = 0; i < 8; i += 1) {
    const residual = anomaly - eccentricity * Math.sin(anomaly) - mean
    const slope = 1 - eccentricity * Math.cos(anomaly)
    anomaly -= residual / slope
  }
  return anomaly
}

export function orbitalRadius(index: number, time: number): number {
  const orbit = HUB_NODES[index].orbit
  const mean = orbitalFrequency(orbit.semiMajor) * time + orbit.meanAnomaly
  const anomaly = eccentricAnomaly(mean, orbit.eccentricity)
  return orbit.semiMajor * (1 - orbit.eccentricity * Math.cos(anomaly))
}

export function nodePosition(index: number, time: number): [number, number, number] {
  const orbit = HUB_NODES[index].orbit
  const mean = orbitalFrequency(orbit.semiMajor) * time + orbit.meanAnomaly
  const anomaly = eccentricAnomaly(mean, orbit.eccentricity)
  const cosE = Math.cos(anomaly)
  const sinE = Math.sin(anomaly)
  const xOrbit = orbit.semiMajor * (cosE - orbit.eccentricity)
  const yOrbit = orbit.semiMajor * Math.sqrt(1 - orbit.eccentricity * orbit.eccentricity) * sinE

  const cosW = Math.cos(orbit.periapsis)
  const sinW = Math.sin(orbit.periapsis)
  const x1 = xOrbit * cosW - yOrbit * sinW
  const y1 = xOrbit * sinW + yOrbit * cosW

  const cosI = Math.cos(orbit.inclination)
  const sinI = Math.sin(orbit.inclination)
  const y2 = y1 * cosI
  const z2 = y1 * sinI

  const cosO = Math.cos(orbit.ascendingNode)
  const sinO = Math.sin(orbit.ascendingNode)
  return [x1 * cosO - y2 * sinO, x1 * sinO + y2 * cosO, z2]
}

export function deflectionSag(gm: number, hubRadius: number, nodeRadius: number): number {
  const deltaTheta = 2 * gm * (1 / hubRadius - 1 / nodeRadius)
  return deltaTheta * nodeRadius * 0.5 * VISUAL_BEND
}

export function hexToRgb(hex: string): [number, number, number] {
  const value = Number.parseInt(hex.slice(1), 16)
  return [(value >> 16) / 255, ((value >> 8) & 255) / 255, (value & 255) / 255]
}
