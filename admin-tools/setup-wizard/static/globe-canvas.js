/* CPU fallback for this globe's opaque triangle meshes. It consumes the same
 * scene, camera and animation as WebGL. A CPU depth buffer and directional face
 * lighting replace GPU depth, shadow maps and atmospheric shaders; this is
 * deliberately not a general-purpose Three.js renderer. */
export class GlobeCanvasRenderer {
  constructor(T, canvas) {
    this.T = T;
    this.canvas = canvas;
    this.context = canvas.getContext('2d');
    if (!this.context) throw new Error('Canvas rendering is unavailable');
    this.shadowMap = {};
    this.geometryCache = new WeakMap();
    this.colorCache = new WeakMap();
    this.projection = new T.Matrix4();
    this.transform = new T.Matrix4();
    this.normalTransform = new T.Matrix3();
    this.normal = new T.Vector3();
    this.vertex = new T.Vector3();
    this.sun = new T.Vector3(-4, 7, 6).normalize();
    this.rim = new T.Vector3(4, 1, -5).normalize();
  }

  // One pixel per CSS pixel bounds CPU work on high-DPI displays.
  setPixelRatio() {}

  setSize(width, height) {
    this.width = this.canvas.width = Math.round(width);
    this.height = this.canvas.height = Math.round(height);
    this.image = this.context.createImageData(this.width, this.height);
    this.depth = new Float32Array(this.width * this.height);
  }

  triangles(geometry) {
    if (this.geometryCache.has(geometry)) return this.geometryCache.get(geometry);
    const T = this.T, positions = geometry.attributes.position;
    const index = geometry.index;
    const count = index ? index.count : positions.count;
    const triangles = [];
    const a = new T.Vector3(), b = new T.Vector3(), c = new T.Vector3();
    for (let i = 0; i < count; i += 3) {
      const ids = [0, 1, 2].map(offset => index ? index.getX(i + offset) : i + offset);
      a.fromBufferAttribute(positions, ids[0]);
      b.fromBufferAttribute(positions, ids[1]);
      c.fromBufferAttribute(positions, ids[2]);
      const normal = b.sub(a).cross(c.sub(a)).normalize();
      const group = geometry.groups.find(group => i >= group.start && i < group.start + group.count);
      triangles.push({ ids, normal: normal.clone(), materialIndex: group?.materialIndex ?? 0 });
    }
    const cached = { triangles, projected: new Float64Array(positions.count * 3) };
    this.geometryCache.set(geometry, cached);
    return cached;
  }

  shade(material, normal) {
    // Quantizing light levels bounds the palette and avoids allocating a Color
    // for every triangle on every frame. Colors are converted back to sRGB.
    const light = .46 + .72 * Math.max(0, normal.dot(this.sun))
      + .12 * Math.max(0, normal.dot(this.rim)) + (material.emissive?.getHex() ? material.emissiveIntensity * .15 : 0);
    const level = Math.round(light * 24);
    if (!this.colorCache.has(material)) this.colorCache.set(material, new Map());
    const palette = this.colorCache.get(material);
    if (!palette.has(level)) palette.set(level, material.color.clone().multiplyScalar(level / 24).getHex(this.T.SRGBColorSpace));
    return palette.get(level);
  }

  render(scene, camera) {
    const { context: ctx, width, height, T } = this;
    scene.updateMatrixWorld();
    camera.updateMatrixWorld();
    this.projection.multiplyMatrices(camera.projectionMatrix, camera.matrixWorldInverse);
    const faces = [];
    scene.traverseVisible(object => {
      if (!object.isMesh) return;
      const materials = Array.isArray(object.material) ? object.material : [object.material];
      // Transparent water overlays and the atmospheric shader are GPU effects.
      if (materials.every(m => !m.visible || !m.color || m.opacity < .5)) return;
      const geometry = object.geometry, positions = geometry.attributes.position;
      if (!positions) return;
      const { triangles, projected } = this.triangles(geometry);
      this.transform.multiplyMatrices(this.projection, object.matrixWorld);
      this.normalTransform.getNormalMatrix(object.matrixWorld);
      for (let i = 0; i < positions.count; i++) {
        this.vertex.fromBufferAttribute(positions, i).applyMatrix4(this.transform);
        projected[i * 3] = (this.vertex.x + 1) * width / 2;
        projected[i * 3 + 1] = (1 - this.vertex.y) * height / 2;
        projected[i * 3 + 2] = this.vertex.z;
      }
      for (const triangle of triangles) {
        const material = materials[triangle.materialIndex] || materials[0];
        if (!material.visible || !material.color || material.opacity < .5) continue;
        const [a, b, c] = triangle.ids.map(id => id * 3);
        const ax = projected[a], ay = projected[a + 1], az = projected[a + 2];
        const bx = projected[b], by = projected[b + 1], bz = projected[b + 2];
        const cx = projected[c], cy = projected[c + 1], cz = projected[c + 2];
        if (az < -1 || bz < -1 || cz < -1 || az > 1 || bz > 1 || cz > 1) continue;
        const area = (bx - ax) * (cy - ay) - (by - ay) * (cx - ax);
        if (Math.abs(area) < .01 || (material.side === T.FrontSide && area >= 0)
          || (material.side === T.BackSide && area < 0)) continue;
        if (Math.max(ax, bx, cx) < 0 || Math.min(ax, bx, cx) > width
          || Math.max(ay, by, cy) < 0 || Math.min(ay, by, cy) > height) continue;
        this.normal.copy(triangle.normal).applyMatrix3(this.normalTransform).normalize();
        faces.push({ ax, ay, az, bx, by, bz, cx, cy, cz, color: this.shade(material, this.normal) });
      }
    });
    this.image.data.fill(0);
    this.depth.fill(Infinity);
    for (const face of faces) this.rasterize(face);
    ctx.putImageData(this.image, 0, 0);
  }

  rasterize({ ax, ay, az, bx, by, bz, cx, cy, cz, color }) {
    const { width, height, depth } = this;
    const pixels = this.image.data;
    const left = Math.max(0, Math.floor(Math.min(ax, bx, cx)));
    const right = Math.min(width - 1, Math.ceil(Math.max(ax, bx, cx)));
    const top = Math.max(0, Math.floor(Math.min(ay, by, cy)));
    const bottom = Math.min(height - 1, Math.ceil(Math.max(ay, by, cy)));
    const denominator = (by - cy) * (ax - cx) + (cx - bx) * (ay - cy);
    const stepA = (by - cy) / denominator;
    const stepB = (cy - ay) / denominator;
    const red = (color >> 16) & 255, green = (color >> 8) & 255, blue = color & 255;
    for (let y = top; y <= bottom; y++) {
      let a = ((by - cy) * (left + .5 - cx) + (cx - bx) * (y + .5 - cy)) / denominator;
      let b = ((cy - ay) * (left + .5 - cx) + (ax - cx) * (y + .5 - cy)) / denominator;
      for (let x = left; x <= right; x++, a += stepA, b += stepB) {
        const c = 1 - a - b;
        if (a < -1e-7 || b < -1e-7 || c < -1e-7) continue;
        const z = a * az + b * bz + c * cz, index = y * width + x;
        if (z >= depth[index]) continue;
        depth[index] = z;
        const offset = index * 4;
        pixels[offset] = red;
        pixels[offset + 1] = green;
        pixels[offset + 2] = blue;
        pixels[offset + 3] = 255;
      }
    }
  }
}
