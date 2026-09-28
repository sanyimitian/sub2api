import { describe, expect, it } from 'vitest'
import {
  GM,
  HUB_NODES,
  HUB_RADIUS,
  VISUAL_BEND,
  deflectionSag,
  nodePosition,
  orbitalFrequency,
  orbitalRadius,
} from '../hubPhysics'

describe('relay hub orbits', () => {
  it('places six model nodes on ellipses with faster inner orbits', () => {
    expect(HUB_NODES).toHaveLength(6)
    const inner = HUB_NODES[0].orbit.semiMajor
    const outer = HUB_NODES[5].orbit.semiMajor
    expect(outer).toBeGreaterThan(inner)
    expect(orbitalFrequency(outer)).toBeLessThan(orbitalFrequency(inner))
  })

  it('uses the Kepler mean motion n² = GM / a³', () => {
    const semiMajor = HUB_NODES[2].orbit.semiMajor
    const motion = orbitalFrequency(semiMajor)
    expect(motion * motion).toBeCloseTo(GM / (semiMajor * semiMajor * semiMajor))
  })

  it('sits at periapsis when the mean anomaly is zero', () => {
    const orbit = HUB_NODES[0].orbit
    const [x, y, z] = nodePosition(0, 0)
    const radius = Math.hypot(x, y, z)
    expect(radius).toBeCloseTo(orbit.semiMajor * (1 - orbit.eccentricity))
    expect(orbitalRadius(0, 0)).toBeCloseTo(radius)
  })

  it('keeps later positions between periapsis and apoapsis', () => {
    const orbit = HUB_NODES[3].orbit
    const radius = orbitalRadius(3, 4.2)
    expect(radius).toBeGreaterThan(orbit.semiMajor * (1 - orbit.eccentricity) - 1e-6)
    expect(radius).toBeLessThan(orbit.semiMajor * (1 + orbit.eccentricity) + 1e-6)
    const [x, y, z] = nodePosition(3, 4.2)
    expect(Math.hypot(x, y, z)).toBeCloseTo(radius)
  })

  it('bends filaments by the weak-field deflection', () => {
    const nodeRadius = 4
    const sag = deflectionSag(GM, HUB_RADIUS, nodeRadius)
    const deltaTheta = 2 * GM * (1 / HUB_RADIUS - 1 / nodeRadius)
    expect(sag).toBeCloseTo(deltaTheta * nodeRadius * 0.5 * VISUAL_BEND)
    expect(sag).toBeGreaterThan(0.2)
    expect(sag).toBeLessThan(nodeRadius)
  })
})