import * as THREE from 'three';
import { COLOR_SCHEME, ANIMATION_CONFIG, QUALITY_PRESETS } from '@/config/threeConfig';

export function useThreeOrb(scene, camera) {
  let meshes = [];
  let materials = [];
  let grid = null;
  let coreSphere = null;
  let satellites = [];
  let orbitRings = [];
  let particles = null;
  let currentQuality = 2;
  let particleFlow = 0;
  let isCollapsed = false;

  // 4 个卫星位置（圆周分布）
  const SATELLITE_ANGLES = [0, Math.PI / 2, Math.PI, Math.PI * 1.5];
  const ORBIT_RADIUS = 60;

  function init(qualityLevel = 2) {
    currentQuality = qualityLevel;
    const preset = getQualityPreset(qualityLevel);

    // 设置相机位置
    camera.position.set(0, 30, 200);
    camera.lookAt(0, 0, 0);

    // 创建球面弯曲网格
    grid = createGrid(preset.gridSize);
    scene.add(grid);
    meshes.push(grid);

    // 创建中心发光球体
    coreSphere = createCoreSphere();
    scene.add(coreSphere.group);
    meshes.push(coreSphere.group);

    // 创建 4 个卫星节点
    SATELLITE_ANGLES.forEach((angle, index) => {
      const satellite = createSatellite(angle, index);
      satellites.push(satellite);
      scene.add(satellite.group);
      meshes.push(satellite.group);
    });

    // 创建轨道环
    SATELLITE_ANGLES.forEach((angle, index) => {
      const ring = createOrbitRing(ORBIT_RADIUS);
      orbitRings.push(ring);
      scene.add(ring);
      meshes.push(ring);
    });

    // 创建轨道粒子流
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
    const color = new THREE.Color(COLOR_SCHEME.gridLine);
    const curvature = 0.3; // 球面弯曲系数

    // 横线（带曲率）
    for (let i = 0; i <= gridSize; i++) {
      const z = -80 - (i * (40 / gridSize));
      const segments = 20;
      
      for (let j = 0; j < segments; j++) {
        const t1 = j / segments;
        const t2 = (j + 1) / segments;
        const x1 = -halfSize + t1 * (halfSize * 2);
        const x2 = -halfSize + t2 * (halfSize * 2);
        
        // 球面弯曲
        const y1 = -Math.pow(Math.abs(x1 / halfSize), 2) * curvature * 20;
        const y2 = -Math.pow(Math.abs(x2 / halfSize), 2) * curvature * 20;
        
        vertices.push(x1, y1, z, x2, y2, z);
        colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
      }
    }

    // 竖线（带曲率）
    for (let i = 0; i <= gridSize; i++) {
      const x = -halfSize + (i * step);
      const segments = 20;
      
      for (let j = 0; j < segments; j++) {
        const t1 = j / segments;
        const t2 = (j + 1) / segments;
        const z1 = -80 - t1 * 40;
        const z2 = -80 - t2 * 40;
        
        const y1 = -Math.pow(Math.abs(x / halfSize), 2) * curvature * 20;
        const y2 = -Math.pow(Math.abs(x / halfSize), 2) * curvature * 20;
        
        vertices.push(x, y1, z1, x, y2, z2);
        colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
      }
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

  function createCoreSphere() {
    const group = new THREE.Group();

    // 内核
    const coreGeometry = new THREE.SphereGeometry(15, 32, 32);
    const coreMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.8,
    });
    materials.push(coreMaterial);

    const core = new THREE.Mesh(coreGeometry, coreMaterial);
    group.add(core);

    // 光晕
    const glowGeometry = new THREE.SphereGeometry(20, 32, 32);
    const glowMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.3,
    });
    materials.push(glowMaterial);

    const glow = new THREE.Mesh(glowGeometry, glowMaterial);
    group.add(glow);

    group.userData.sphere = {
      core,
      glow,
      targetScale: 1,
      currentScale: 1,
      pulseAmplitude: 0.1,
    };

    return { group, core, glow };
  }

  function createSatellite(angle, index) {
    const group = new THREE.Group();
    
    const x = Math.cos(angle) * ORBIT_RADIUS;
    const z = Math.sin(angle) * ORBIT_RADIUS;
    group.position.set(x, 0, z);

    // 卫星球体
    const geometry = new THREE.SphereGeometry(4, 16, 16);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.7,
    });
    materials.push(material);

    const mesh = new THREE.Mesh(geometry, material);
    group.add(mesh);

    // 光晕
    const glowGeometry = new THREE.SphereGeometry(6, 16, 16);
    const glowMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.2,
    });
    materials.push(glowMaterial);

    const glow = new THREE.Mesh(glowGeometry, glowMaterial);
    group.add(glow);

    group.userData.satellite = {
      mesh,
      glow,
      index,
      angle,
      baseRadius: ORBIT_RADIUS,
      targetRadius: ORBIT_RADIUS,
      currentRadius: ORBIT_RADIUS,
      targetScale: 1,
      currentScale: 1,
    };

    return { group, mesh, glow };
  }

  function createOrbitRing(radius) {
    const geometry = new THREE.RingGeometry(radius - 1, radius + 1, 64);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.gridLine),
      transparent: true,
      opacity: 0.2,
      side: THREE.DoubleSide,
    });
    materials.push(material);

    const ring = new THREE.Mesh(geometry, material);
    ring.rotation.x = Math.PI / 2;

    return ring;
  }

  function createParticles(count) {
    const geometry = new THREE.BufferGeometry();
    const positions = [];
    const colors = [];
    const velocities = [];

    const color = new THREE.Color(COLOR_SCHEME.particle);

    for (let i = 0; i < count; i++) {
      const angle = Math.random() * Math.PI * 2;
      const radius = ORBIT_RADIUS + (Math.random() - 0.5) * 10;
      
      positions.push(
        Math.cos(angle) * radius,
        (Math.random() - 0.5) * 10,
        Math.sin(angle) * radius
      );
      
      colors.push(color.r, color.g, color.b);
      velocities.push(Math.random() > 0.5 ? 1 : -1); // 顺时针或逆时针
    }

    geometry.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
    geometry.setAttribute('velocity', new THREE.Float32BufferAttribute(velocities, 1));

    const material = new THREE.PointsMaterial({
      size: 2,
      vertexColors: true,
      transparent: true,
      opacity: 0.7,
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

    // 更新中心球
    if (coreSphere && data) {
      updateCoreSphere(data, deltaTime);
    }

    // 更新卫星
    if (data) {
      updateSatellites(data, deltaTime);
    }

    // 更新轨道环
    if (data?.loading) {
      updateOrbitRings(deltaTime);
    }

    // 更新粒子流
    if (particles) {
      updateParticles(deltaTime, data?.updated);
    }
  }

  function updateGrid(time) {
    if (!grid) return;

    const positions = grid.geometry.attributes.position;
    const array = positions.array;

    for (let i = 0; i < array.length; i += 3) {
      const x = array[i];
      const z = array[i + 2];
      const wave = Math.sin(x * 0.05 + time) * ANIMATION_CONFIG.gridWaveAmplitude +
                   Math.cos(z * 0.05 + time * 0.5) * ANIMATION_CONFIG.gridWaveAmplitude;
      array[i + 1] += wave * 0.01;
    }

    positions.needsUpdate = true;
  }

  function updateCoreSphere(data, time) {
    const sphereData = coreSphere.group.userData.sphere;
    const displayRate = data.cacheHitRate || 0;
    
    sphereData.targetScale = 1 + displayRate * 0.2;
    sphereData.currentScale += (sphereData.targetScale - sphereData.currentScale) * 0.1;

    // 脉冲动画
    const pulse = Math.sin(time * 3) * sphereData.pulseAmplitude * displayRate;
    const scale = sphereData.currentScale + pulse;
    
    sphereData.core.scale.setScalar(scale);
    sphereData.glow.scale.setScalar(scale * 1.5);

    // 光晕半径随比率增加
    sphereData.glow.material.opacity = 0.2 + displayRate * 0.2;
  }

  function updateSatellites(data, time) {
    const values = [
      Math.log10(Math.max(data.totalTokens || 1, 1)) / 6,
      Math.log10(Math.max(data.turnsTotal || 1, 1)) / 4,
      Math.log10(Math.max(data.localCacheHits || 1, 1)) / 4,
      Math.log10(Math.max(data.promptTokens || 1, 1)) / 6,
    ];

    satellites.forEach((satGroup, index) => {
      const satData = satGroup.group.userData.satellite;
      
      // 大小变化
      satData.targetScale = 0.7 + values[index] * 0.8;
      satData.currentScale += (satData.targetScale - satData.currentScale) * 0.1;
      
      satData.mesh.scale.setScalar(satData.currentScale);
      satData.glow.scale.setScalar(satData.currentScale * 1.5);

      // 轨道半径动态变化
      satData.targetRadius = satData.baseRadius + values[index] * 10;
      satData.currentRadius += (satData.targetRadius - satData.currentRadius) * 0.05;

      const x = Math.cos(satData.angle) * satData.currentRadius;
      const z = Math.sin(satData.angle) * satData.currentRadius;
      satGroup.group.position.set(x, 0, z);
    });
  }

  function updateOrbitRings(time) {
    orbitRings.forEach((ring, index) => {
      ring.rotation.z = time * (index % 2 === 0 ? 0.3 : -0.3);
    });
  }

  function updateParticles(time, isUpdated) {
    if (!particles) return;

    const positions = particles.geometry.attributes.position;
    const velocities = particles.geometry.attributes.velocity;
    const array = positions.array;
    const velArray = velocities.array;

    const speed = isUpdated ? 
      ANIMATION_CONFIG.particleSpeed * 2 :
      ANIMATION_CONFIG.particleSpeed;

    for (let i = 0; i < array.length / 3; i++) {
      const idx = i * 3;
      const x = array[idx];
      const z = array[idx + 2];
      
      const angle = Math.atan2(z, x);
      const radius = Math.sqrt(x * x + z * z);
      
      const newAngle = angle + velArray[i] * speed * 0.01;
      
      array[idx] = Math.cos(newAngle) * radius;
      array[idx + 2] = Math.sin(newAngle) * radius;
    }

    positions.needsUpdate = true;
  }

  function setCollapsed(collapsed) {
    isCollapsed = collapsed;

    if (coreSphere) {
      coreSphere.group.visible = !collapsed;
    }

    satellites.forEach(satGroup => {
      satGroup.group.visible = !collapsed;
    });

    orbitRings.forEach(ring => {
      ring.visible = !collapsed;
    });

    if (particles) {
      if (collapsed) {
        particles.geometry.setDrawRange(0, Math.floor(particles.userData.fullCount * 0.3));
        particles.material.opacity = 0.3;
      } else {
        particles.geometry.setDrawRange(0, particles.userData.fullCount);
        particles.material.opacity = 0.7;
      }
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
    satellites = [];
    orbitRings = [];
    grid = null;
    coreSphere = null;
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