import * as THREE from 'three';
import { COLOR_SCHEME, ANIMATION_CONFIG, QUALITY_PRESETS } from '@/config/threeConfig';

export function useThreeEngine(scene, camera) {
  let meshes = [];
  let materials = [];
  let grid = null;
  let ring = null;
  let nodes = [];
  let scanLine = null;
  let particles = null;
  let currentQuality = 2;
  let scanLineRotation = 0;
  let isCollapsed = false;

  // 4 个节点位置（圆周分布）
  const NODE_ANGLES = [0, Math.PI / 2, Math.PI, Math.PI * 1.5];
  const NODE_RADIUS = 70;

  function init(qualityLevel = 2) {
    currentQuality = qualityLevel;
    const preset = getQualityPreset(qualityLevel);

    // 设置相机位置
    camera.position.set(0, 60, 180);
    camera.lookAt(0, 0, 0);

    // 创建网格背景（橙调）
    grid = createGrid(preset.gridSize);
    scene.add(grid);
    meshes.push(grid);

    // 创建中心环形结构
    ring = createRing();
    scene.add(ring.group);
    meshes.push(ring.group);

    // 创建 4 个浮动数据节点
    NODE_ANGLES.forEach((angle, index) => {
      const node = createNode(angle, index);
      nodes.push(node);
      scene.add(node.group);
      meshes.push(node.group);
    });

    // 创建旋转扫描线
    scanLine = createScanLine();
    scene.add(scanLine);
    meshes.push(scanLine);

    // 创建环内粒子
    particles = createParticles(preset.particleCount);
    scene.add(particles);
    meshes.push(particles);
  }

  function createGrid(gridSize) {
    const geometry = new THREE.BufferGeometry();
    const vertices = [];
    const colors = [];
    
    const step = 10;
    const halfSize = (gridSize * step) / 2;
    // 橙调网格
    const color = new THREE.Color(COLOR_SCHEME.gridLine).multiplyScalar(1.2);
    color.r = Math.min(color.r * 1.3, 1);

    // 横线
    for (let i = 0; i <= gridSize; i++) {
      const z = -80 - (i * (40 / gridSize));
      vertices.push(-halfSize, 0, z, halfSize, 0, z);
      colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
    }

    // 竖线
    for (let i = 0; i <= gridSize; i++) {
      const x = -halfSize + (i * step);
      vertices.push(x, 0, -80, x, 0, -120);
      colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
    }

    geometry.setAttribute('position', new THREE.Float32BufferAttribute(vertices, 3));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));

    const material = new THREE.LineBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: 0.5,
    });
    materials.push(material);

    return new THREE.LineSegments(geometry, material);
  }

  function createRing() {
    const group = new THREE.Group();

    // 外环
    const outerGeometry = new THREE.RingGeometry(38, 42, 64);
    const outerMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.muted),
      transparent: true,
      opacity: 0.3,
      side: THREE.DoubleSide,
    });
    materials.push(outerMaterial);

    const outerRing = new THREE.Mesh(outerGeometry, outerMaterial);
    outerRing.rotation.x = Math.PI / 2;
    group.add(outerRing);

    // 填充环（动态角度）
    const fillGeometry = new THREE.RingGeometry(0, 40, 64, 1, 0, Math.PI * 2);
    const fillMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.5,
      side: THREE.DoubleSide,
    });
    materials.push(fillMaterial);

    const fillRing = new THREE.Mesh(fillGeometry, fillMaterial);
    fillRing.rotation.x = Math.PI / 2;
    group.add(fillRing);

    group.userData.ring = {
      outer: outerRing,
      fill: fillRing,
      targetAngle: Math.PI * 2,
      currentAngle: Math.PI * 2,
    };

    return { group, outer: outerRing, fill: fillRing };
  }

  function createNode(angle, index) {
    const group = new THREE.Group();
    
    const x = Math.cos(angle) * NODE_RADIUS;
    const z = Math.sin(angle) * NODE_RADIUS;
    group.position.set(x, 0, z);

    // 节点球体
    const geometry = new THREE.SphereGeometry(5, 16, 16);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.8,
    });
    materials.push(material);

    const mesh = new THREE.Mesh(geometry, material);
    group.add(mesh);

    // 光晕
    const glowGeometry = new THREE.SphereGeometry(8, 16, 16);
    const glowMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.2,
    });
    materials.push(glowMaterial);

    const glow = new THREE.Mesh(glowGeometry, glowMaterial);
    group.add(glow);

    group.userData.node = {
      mesh,
      glow,
      index,
      angle,
      targetScale: 1,
      currentScale: 1,
      pulsePhase: index * Math.PI / 2,
    };

    return { group, mesh, glow };
  }

  function createScanLine() {
    const geometry = new THREE.PlaneGeometry(80, 2);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.scanLine),
      transparent: true,
      opacity: 0.7,
      side: THREE.DoubleSide,
    });
    materials.push(material);

    const line = new THREE.Mesh(geometry, material);
    line.rotation.x = Math.PI / 2;
    line.position.y = 0;

    return line;
  }

  function createParticles(count) {
    const geometry = new THREE.BufferGeometry();
    const positions = [];
    const colors = [];

    const color = new THREE.Color(COLOR_SCHEME.particle);

    for (let i = 0; i < count; i++) {
      const angle = Math.random() * Math.PI * 2;
      const radius = Math.random() * 40;
      positions.push(
        Math.cos(angle) * radius,
        (Math.random() - 0.5) * 5,
        Math.sin(angle) * radius
      );
      colors.push(color.r, color.g, color.b);
    }

    geometry.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));

    const material = new THREE.PointsMaterial({
      size: 1.5,
      vertexColors: true,
      transparent: true,
      opacity: 0.6,
    });
    materials.push(material);

    const particleSystem = new THREE.Points(geometry, material);
    particleSystem.userData.fullCount = count;

    return particleSystem;
  }

  function update(data, time) {
    const deltaTime = time * 0.001;

    // 更新网格
    if (grid && !isCollapsed) {
      updateGrid(deltaTime);
    }

    // 更新中心环
    if (ring && data) {
      updateRing(data);
    }

    // 更新节点
    if (data) {
      updateNodes(data, deltaTime);
    }

    // 更新扫描线
    if (scanLine) {
      updateScanLine(deltaTime, data?.updated);
    }

    // 更新粒子
    if (particles && data) {
      updateParticles(deltaTime, data);
    }
  }

  function updateGrid(time) {
    if (!grid) return;

    const positions = grid.geometry.attributes.position;
    const array = positions.array;

    for (let i = 0; i < array.length; i += 3) {
      const x = array[i];
      const z = array[i + 2];
      array[i + 1] = Math.sin(x * 0.05 + time) * ANIMATION_CONFIG.gridWaveAmplitude +
                     Math.cos(z * 0.05 + time * 0.5) * ANIMATION_CONFIG.gridWaveAmplitude;
    }

    positions.needsUpdate = true;
  }

  function updateRing(data) {
    const ringData = ring.group.userData.ring;
    const displayRate = data.cacheHitRate || 0;
    
    ringData.targetAngle = displayRate * Math.PI * 2;
    ringData.currentAngle += (ringData.targetAngle - ringData.currentAngle) * 0.1;

    // 重建填充环几何体
    ring.fill.geometry.dispose();
    ring.fill.geometry = new THREE.RingGeometry(0, 40, 64, 1, 0, ringData.currentAngle);

    // 调整发光度
    ring.fill.material.opacity = 0.3 + displayRate * 0.3;
  }

  function updateNodes(data, time) {
    const values = [
      Math.log10(Math.max(data.totalTokens || 1, 1)) / 6,
      Math.log10(Math.max(data.turnsTotal || 1, 1)) / 4,
      Math.log10(Math.max(data.localCacheHits || 1, 1)) / 4,
      Math.log10(Math.max(data.promptTokens || 1, 1)) / 6,
    ];

    nodes.forEach((nodeGroup, index) => {
      const nodeData = nodeGroup.group.userData.node;
      nodeData.targetScale = 0.8 + values[index] * 0.8; // 0.8 - 1.6
      
      nodeData.currentScale += (nodeData.targetScale - nodeData.currentScale) * 0.1;
      nodeData.mesh.scale.setScalar(nodeData.currentScale);

      // 脉冲动画
      const pulse = Math.sin(time * 2 + nodeData.pulsePhase) * 0.1 + 0.9;
      nodeData.glow.scale.setScalar(nodeData.currentScale * pulse * 1.3);
    });
  }

  function updateScanLine(time, isUpdated) {
    if (!scanLine) return;

    const speed = isUpdated ? 2.0 : 0.5;
    scanLineRotation += speed * 0.016; // 约 60fps

    scanLine.rotation.z = scanLineRotation;
  }

  function updateParticles(time, data) {
    if (!particles) return;

    const displayRate = data.cacheHitRate || 0;
    particles.material.opacity = 0.4 + displayRate * 0.3;
  }

  function setCollapsed(collapsed) {
    isCollapsed = collapsed;

    if (ring) {
      ring.group.visible = !collapsed;
    }

    nodes.forEach(nodeGroup => {
      nodeGroup.group.visible = !collapsed;
    });

    if (particles) {
      if (collapsed) {
        particles.geometry.setDrawRange(0, Math.floor(particles.userData.fullCount * 0.3));
        particles.material.opacity = 0.3;
      } else {
        particles.geometry.setDrawRange(0, particles.userData.fullCount);
      }
    }

    if (scanLine) {
      scanLine.material.opacity = collapsed ? 0.3 : 0.7;
    }
  }

  function adjustQuality(level) {
    if (level === currentQuality) return;
    
    currentQuality = level;
    const preset = getQualityPreset(level);

    // 重建网格
    if (grid) {
      scene.remove(grid);
      grid.geometry.dispose();
      grid.material.dispose();
      
      grid = createGrid(preset.gridSize);
      scene.add(grid);
    }

    // 调整粒子
    if (particles) {
      scene.remove(particles);
      particles.geometry.dispose();
      particles.material.dispose();
      
      particles = createParticles(preset.particleCount);
      scene.add(particles);
    }
  }

  function getQualityPreset(level) {
    if (level === 2) return QUALITY_PRESETS.high;
    if (level === 1) return QUALITY_PRESETS.medium;
    return QUALITY_PRESETS.low;
  }

  function dispose() {
    meshes.forEach(m => {
      if (m.geometry) m.geometry.dispose();
      if (m.material) {
        if (Array.isArray(m.material)) {
          m.material.forEach(mat => mat.dispose());
        } else {
          m.material.dispose();
        }
      }
      scene.remove(m);
    });

    materials.forEach(m => m.dispose());

    meshes = [];
    materials = [];
    nodes = [];
    grid = null;
    ring = null;
    scanLine = null;
    particles = null;
  }

  return {
    init,
    update,
    setCollapsed,
    adjustQuality,
    dispose,
  };
}