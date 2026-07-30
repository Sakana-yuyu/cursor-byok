<script setup>
import * as THREE from "three";
import { onMounted, onUnmounted, ref } from "vue";

const host = ref(null);

let renderer = null;
let scene = null;
let camera = null;
let chart = null;
let bars = [];
let animationFrame = 0;
let startedAt = 0;
let reduceMotion = false;

function buildChart() {
  chart = new THREE.Group();
  chart.rotation.x = -0.16;
  scene.add(chart);

  const baseMaterial = new THREE.MeshStandardMaterial({
    color: 0x102a28,
    metalness: 0.35,
    roughness: 0.42,
    transparent: true,
    opacity: 0.78,
  });
  const base = new THREE.Mesh(new THREE.BoxGeometry(1.72, 0.08, 0.72), baseMaterial);
  base.position.y = -0.68;
  chart.add(base);

  const heights = [0.72, 1.08, 0.88];
  const colors = [0x48e6a2, 0x85f7ca, 0x35cfe0];
  bars = heights.map((height, index) => {
    const material = new THREE.MeshStandardMaterial({
      color: colors[index],
      emissive: colors[index],
      emissiveIntensity: 0.16,
      metalness: 0.18,
      roughness: 0.28,
    });
    const bar = new THREE.Mesh(new THREE.BoxGeometry(0.3, 1, 0.38), material);
    bar.position.set((index - 1) * 0.5, -0.63 + height / 2, 0);
    bar.scale.y = height;
    bar.userData.baseHeight = height;
    chart.add(bar);
    return bar;
  });

  const trendPoints = [
    new THREE.Vector3(-0.65, 0.04, 0.28),
    new THREE.Vector3(-0.18, 0.38, 0.28),
    new THREE.Vector3(0.28, 0.2, 0.28),
    new THREE.Vector3(0.7, 0.62, 0.28),
  ];
  const trend = new THREE.Line(
    new THREE.BufferGeometry().setFromPoints(trendPoints),
    new THREE.LineBasicMaterial({ color: 0xd8fff0, transparent: true, opacity: 0.9 }),
  );
  chart.add(trend);
}

function renderFrame(timestamp) {
  if (!renderer || !scene || !camera) return;

  const elapsed = (timestamp - startedAt) / 1000;
  chart.rotation.y = Math.sin(elapsed * 0.7) * 0.2;
  bars.forEach((bar, index) => {
    const height = bar.userData.baseHeight * (1 + Math.sin(elapsed * 1.8 + index * 0.9) * 0.055);
    bar.scale.y = height;
    bar.position.y = -0.63 + height / 2;
  });
  renderer.render(scene, camera);

  if (!reduceMotion && !document.hidden) {
    animationFrame = requestAnimationFrame(renderFrame);
  }
}

function startRendering() {
  if (!renderer || animationFrame || document.hidden) return;
  startedAt = performance.now();
  animationFrame = requestAnimationFrame((timestamp) => {
    animationFrame = 0;
    renderFrame(timestamp);
  });
}

function handleVisibilityChange() {
  if (document.hidden) {
    cancelAnimationFrame(animationFrame);
    animationFrame = 0;
    return;
  }
  startRendering();
}

function disposeScene() {
  cancelAnimationFrame(animationFrame);
  animationFrame = 0;
  document.removeEventListener("visibilitychange", handleVisibilityChange);

  scene?.traverse((object) => {
    object.geometry?.dispose();
    if (Array.isArray(object.material)) {
      object.material.forEach((material) => material.dispose());
    } else {
      object.material?.dispose();
    }
  });
  renderer?.dispose();
  renderer?.domElement.remove();
  renderer = null;
  scene = null;
  camera = null;
  chart = null;
  bars = [];
}

onMounted(() => {
  if (!host.value) return;

  reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  scene = new THREE.Scene();
  camera = new THREE.OrthographicCamera(-1.15, 1.15, 1.15, -1.15, 0.1, 10);
  camera.position.set(0, 0.08, 4);

  renderer = new THREE.WebGLRenderer({ alpha: true, antialias: true, powerPreference: "low-power" });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5));
  renderer.setSize(44, 44, false);
  renderer.setClearColor(0x000000, 0);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  host.value.appendChild(renderer.domElement);

  scene.add(new THREE.HemisphereLight(0xd9fff2, 0x0a1b20, 2.2));
  const keyLight = new THREE.DirectionalLight(0xffffff, 2.4);
  keyLight.position.set(2, 3, 4);
  scene.add(keyLight);
  buildChart();

  document.addEventListener("visibilitychange", handleVisibilityChange);
  startRendering();
});

onUnmounted(disposeScene);
</script>

<template>
  <div ref="host" class="stats-pulse-icon" aria-hidden="true"></div>
</template>

<style scoped>
.stats-pulse-icon {
  width: 44px;
  height: 44px;
  filter: drop-shadow(0 3px 6px rgba(0, 0, 0, .32));
}

.stats-pulse-icon :deep(canvas) {
  display: block;
  width: 44px;
  height: 44px;
}
</style>