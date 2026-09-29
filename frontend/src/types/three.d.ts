declare module 'three' {
  export const LinearSRGBColorSpace: string
  export const NoToneMapping: number
  export const AdditiveBlending: number
  export const NormalBlending: number
  export const FrontSide: number
  export const BackSide: number
  export const DoubleSide: number
  export const LinearFilter: number

  export class Vector2 {
    constructor(x?: number, y?: number)
    set(x: number, y: number): this
  }

  export class Vector3 {
    x: number
    y: number
    z: number
    constructor(x?: number, y?: number, z?: number)
    set(x: number, y: number, z: number): this
    copy(v: Vector3): this
    add(v: Vector3): this
    sub(v: Vector3): this
    multiplyScalar(s: number): this
    normalize(): this
    length(): number
    cross(v: Vector3): this
    dot(v: Vector3): number
    project(camera: Camera): this
  }

  export class Euler {
    x: number
    y: number
    z: number
    set(x: number, y: number, z: number): this
  }

  export class Object3D {
    position: Vector3
    scale: Vector3
    rotation: Euler
    renderOrder: number
    frustumCulled: boolean
    visible: boolean
    add(object: Object3D): this
    lookAt(x: number, y: number, z: number): void
  }

  export class Scene extends Object3D {}

  export class Group extends Object3D {}

  export class Camera extends Object3D {}

  export class OrthographicCamera extends Camera {
    constructor(left: number, right: number, top: number, bottom: number, near?: number, far?: number)
  }

  export class PerspectiveCamera extends Camera {
    aspect: number
    constructor(fov?: number, aspect?: number, near?: number, far?: number)
    updateProjectionMatrix(): void
  }

  export class BufferAttribute {
    count: number
    needsUpdate: boolean
    constructor(array: ArrayLike<number>, itemSize: number)
    getX(index: number): number
    getY(index: number): number
    getZ(index: number): number
    setX(index: number, x: number): this
    setXYZ(index: number, x: number, y: number, z: number): this
  }

  export class BufferGeometry {
    dispose(): void
    setAttribute(name: string, attribute: BufferAttribute): this
    setIndex(index: BufferAttribute): this
    getAttribute(name: string): BufferAttribute
    computeVertexNormals(): void
  }

  export class PlaneGeometry extends BufferGeometry {
    constructor(width?: number, height?: number)
  }

  export class SphereGeometry extends BufferGeometry {
    constructor(radius?: number, widthSegments?: number, heightSegments?: number)
  }

  export class IcosahedronGeometry extends BufferGeometry {
    constructor(radius?: number, detail?: number)
  }

  export class TorusGeometry extends BufferGeometry {
    constructor(radius?: number, tube?: number, radialSegments?: number, tubularSegments?: number)
  }

  export class RingGeometry extends BufferGeometry {
    constructor(innerRadius?: number, outerRadius?: number, thetaSegments?: number, phiSegments?: number)
  }

  export class Material {
    dispose(): void
  }

  export interface ShaderMaterialParameters {
    uniforms?: Record<string, { value: unknown }>
    vertexShader?: string
    fragmentShader?: string
    depthTest?: boolean
    depthWrite?: boolean
    transparent?: boolean
    blending?: number
    side?: number
    toneMapped?: boolean
  }

  export class ShaderMaterial extends Material {
    uniforms: Record<string, { value: unknown }>
    constructor(parameters?: ShaderMaterialParameters)
  }

  export class Mesh extends Object3D {
    material: Material
    constructor(geometry?: BufferGeometry, material?: Material)
  }

  export class Line extends Object3D {
    material: Material
    constructor(geometry?: BufferGeometry, material?: Material)
  }

  export class Texture {
    dispose(): void
  }

  export class WebGLRenderTarget {
    texture: Texture
    constructor(width?: number, height?: number, options?: {
      minFilter?: number
      magFilter?: number
      depthBuffer?: boolean
    })
    setSize(width: number, height: number): void
    dispose(): void
  }

  export class Points extends Object3D {
    constructor(geometry?: BufferGeometry, material?: Material)
  }

  export class WebGLRenderer {
    constructor(parameters?: {
      canvas?: HTMLCanvasElement
      antialias?: boolean
      alpha?: boolean
      powerPreference?: WebGLPowerPreference
    })
    outputColorSpace: string
    toneMapping: number
    setPixelRatio(value: number): void
    setSize(width: number, height: number, updateStyle?: boolean): void
    getDrawingBufferSize(target: Vector2): Vector2
    setClearColor(color: number, alpha?: number): void
    setRenderTarget(target: WebGLRenderTarget | null): void
    render(scene: Scene, camera: Camera): void
    dispose(): void
    getContext(): WebGLRenderingContext | WebGL2RenderingContext | null
  }
}
