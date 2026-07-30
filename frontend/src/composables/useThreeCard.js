import * as THREE from 'three';
import { COLOR_SCHEME, ANIMATION_CONFIG, QUALITY_PRESETS } from '@/config/threeConfig';

export function useThreeCard(scene, camera) {
  let meshes = [];
  let materials = [];
  let grid = null;
  let dataBars = [];
  let scanLine = null;
  let particles = null;
  let currentQuality = 2;
  let scanLineOffset = 0;
  let isCollapsed = false;

  // 数据柱位置
  const BAR_POSITIONS = [-60, -20, 20, 60];

  function init(qualityLevel = 2) {
    currentQuality = qualityLevel;
    const preset = getQualityPreset(qualityLevel);

    // 设置相机位置
    camera.position.set(0, 40, 150);
    camera.lookAt(0, 0, 0);

    // 创建网格背景
    grid = createGrid(preset.gridSize);
    scene.add(grid);
    meshes.push(grid);

    // 创建 4 组数据柱
    BAR_POSITIONS.forEach((x, index) => {
      const bar = createDataBar(x, index);
      dataBars.push(bar);
      scene.add(bar.group);
      meshes.push(bar.group);
    });

    // 创建扫描线
    scanLine = createScanLine();
    scene.add(scanLine);
    meshes.push(scanLine);

    // 创建粒子系统
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
      opacity: 0.6,
    });
    materials.push(material);

    const gridMesh = new THREE.LineSegments(geometry, material);
    gridMesh.userData.isGrid = true;
    return gridMesh;
  }

  function createDataBar(x, index) {
    const group = new THREE.Group();
    group.position.x = x;

    // 柱体 - 大幅降低不透明度，让数据卡片清晰可见
    const geometry = new THREE.BoxGeometry(8, 10, 8);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.15,  // 从 0.7 降到 0.15
    });
    materials.push(material);

    const mesh = new THREE.Mesh(geometry, material);
    mesh.position.y = 5; // 柱体中心在地面以上
    group.add(mesh);

    // 柱体顶部发光点
    const glowGeometry = new THREE.SphereGeometry(2, 16, 16);
    const glowMaterial = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.accent),
      transparent: true,
      opacity: 0.9,
    });
    materials.push(glowMaterial);

    const glow = new THREE.Mesh(glowGeometry, glowMaterial);
    glow.position.y = 10;
    group.add(glow);

    group.userData.bar = {
      mesh,
      glow,
      index,
      targetHeight: 10,
      currentHeight: 10,
    };

    return { group, mesh, glow };
  }

  function createScanLine() {
    const geometry = new THREE.PlaneGeometry(200, 2);
    const material = new THREE.MeshBasicMaterial({
      color: new THREE.Color(COLOR_SCHEME.scanLine),
      transparent: true,
      opacity: 0.6,
      side: THREE.DoubleSide,
    });
    materials.push(material);

    const line = new THREE.Mesh(geometry, material);
    line.rotation.x = Math.PI / 2;
    line.position.z = -80;
    line.userData.isScanLine = true;

    return line;
  }

  function createParticles(count) {
    const geometry = new THREE.BufferGeometry();
    const positions = [];
    const colors = [];
    const sizes = [];

    const color = new THREE.Color(COLOR_SCHEME.particle);

    for (let i = 0; i < count; i++) {
      positions.push(
        (Math.random() - 0.5) * 150,
        Math.random() * 60,
        -80 - Math.random() * 40
      );
      colors.push(color.r, color.g, color.b);
      sizes.push(Math.random() * 2 + 1);
    }

    geometry.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
    geometry.setAttribute('size', new THREE.Float32BufferAttribute(sizes, 1));

    const material = new THREE.PointsMaterial({
      size: 2,
      vertexColors: true,
      transparent: true,
      opacity: 0.6,
      sizeAttenuation: true,
    });
    materials.push(material);

    const particleSystem = new THREE.Points(geometry, material);
    particleSystem.userData.isParticles = true;
    particleSystem.userData.fullCount = count;

    return particleSystem;
  }

  function update(data, time) {
    const deltaTime = time * 0.001; // 转换为秒

    // 更新网格波动
    if (grid && !isCollapsed) {
      updateGrid(deltaTime);
    }

    // 更新数据柱高度
    if (data) {
      updateDataBars(data);
    }

    // 更新扫描线
    if (scanLine) {
      updateScanLine(deltaTime, data?.updated);
    }

    // 更新粒子
    if (particles && !isCollapsed) {
      updateParticles(deltaTime);
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

  function updateDataBars(data) {
    const values = [
      data.cacheHitRate || 0,
      Math.log10(Math.max(data.totalTokens || 1, 1)) / 6, // 归一化到 0-1
      Math.log10(Math.max(data.turnsTotal || 1, 1)) / 4,
      Math.log10(Math.max(data.localCacheHits || 1, 1)) / 4,
    ];

    dataBars.forEach((barGroup, index) => {
      const barData = barGroup.group.userData.bar;
      const targetHeight = 10 + values[index] * 40; // 10-50 范围

      // 平滑过渡
      barData.currentHeight += (targetHeight - barData.currentHeight) * 0.1;
      
      // 更新柱体高度
      barData.mesh.scale.y = barData.currentHeight / 10;
      barData.mesh.position.y = barData.currentHeight / 2;
      
      // 更新顶部发光点
      barData.glow.position.y = barData.currentHeight;

      // 缓存命中率 > 0.3 时变绿色
      if (index === 0 && values[0] > 0.3) {
        barData.mesh.material.color.setStyle(COLOR_SCHEME.accent);
      }
    });
  }

  function updateScanLine(time, isUpdated) {
    if (!scanLine) return;

    const speed = isUpdated ? 
      ANIMATION_CONFIG.scanLineSpeed * ANIMATION_CONFIG.scanLineBoost :
      ANIMATION_CONFIG.scanLineSpeed;

    scanLineOffset += speed;
    scanLine.position.z = -80 - (scanLineOffset % 40);
  }

  function updateParticles(time) {
    if (!particles) return;

    const positions = particles.geometry.attributes.position;
    const array = positions.array;

    for (let i = 0; i < array.length; i += 3) {
      array[i + 1] += ANIMATION_CONFIG.particleSpeed * 0.1;
      
      // 重置到底部
      if (array[i + 1] > 60) {
        array[i + 1] = 0;
      }
    }

    positions.needsUpdate = true;
  }

  function setCollapsed(collapsed) {
    isCollapsed = collapsed;

    dataBars.forEach(barGroup => {
      barGroup.group.visible = !collapsed;
    });

    if (particles) {
      if (collapsed) {
        const fullCount = particles.userData.fullCount;
        particles.geometry.setDrawRange(0, Math.floor(fullCount * 0.3));
        particles.material.opacity = 0.3;
      } else {
        particles.geometry.setDrawRange(0, particles.userData.fullCount);
        particles.material.opacity = 0.6;
      }
    }

    if (scanLine) {
      scanLine.material.opacity = collapsed ? 0.3 : 0.6;
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

    // 调整粒子数量
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
    dataBars = [];
    grid = null;
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