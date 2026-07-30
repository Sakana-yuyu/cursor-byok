// Three.js 浮窗配置参数

export const QUALITY_PRESETS = {
  high: {
    gridSize: 15,
    particleCount: 200,
    antialias: true,
    pixelRatioMax: 2,
  },
  medium: {
    gridSize: 10,
    particleCount: 100,
    antialias: false,
    pixelRatioMax: 1.5,
  },
  low: {
    gridSize: 6,
    particleCount: 50,
    antialias: false,
    pixelRatioMax: 1,
  },
};

export const ANIMATION_CONFIG = {
  scanLineSpeed: 0.5,        // 扫描线基础速度（单位/秒）
  scanLineBoost: 1.5,        // 数据更新时加速倍数
  gridWaveAmplitude: 2.0,    // 网格波动幅度
  particleSpeed: 0.3,        // 粒子移动速度
  transitionDuration: 300,   // 布局切换过渡时长（毫秒）
};

export const COLOR_SCHEME = {
  accent: '#6ee7a5',         // 主题绿色
  muted: '#8b9692',          // 次要灰色
  gridLine: 'rgba(110, 231, 165, 0.08)',     // 网格更透明
  scanLine: 'rgba(110, 231, 165, 0.25)',     // 扫描线更透明
  particle: 'rgba(110, 231, 165, 0.3)',      // 粒子更透明
  dataBar: 'rgba(110, 231, 165, 0.15)',      // 数据柱非常透明
  orb: 'rgba(110, 231, 165, 0.12)',          // Orb 球体极透明
  ring: 'rgba(110, 231, 165, 0.18)',         // 环形稍透明
};

export const CAMERA_POSITIONS = {
  card: { x: 0, y: 40, z: 150 },
  engine: { x: 0, y: 60, z: 180 },
  orb: { x: 0, y: 30, z: 200 },
};