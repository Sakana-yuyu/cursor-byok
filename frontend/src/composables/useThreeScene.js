import * as THREE from 'three';
import { ref, onUnmounted } from 'vue';

export function useThreeScene(containerRef) {
  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(45, 1, 0.1, 1000);
  
  let renderer = null;
  let rafId = null;
  let isPaused = false;
  
  const isInitialized = ref(false);

  function initRenderer(antialias = false) {
    if (renderer) {
      renderer.dispose();
    }

    renderer = new THREE.WebGLRenderer({
      alpha: true,
      antialias,
      powerPreference: 'high-performance',
    });

    renderer.setClearColor(0x000000, 0);
    
    if (containerRef.value) {
      containerRef.value.appendChild(renderer.domElement);
      handleResize();
    }

    isInitialized.value = true;
  }

  function startLoop(callback) {
    if (rafId) return;
    
    const loop = (time) => {
      if (!isPaused && callback) {
        callback(time);
        if (renderer && scene && camera) {
          renderer.render(scene, camera);
        }
      }
      rafId = requestAnimationFrame(loop);
    };
    
    rafId = requestAnimationFrame(loop);
  }

  function pauseLoop() {
    isPaused = true;
  }

  function resumeLoop() {
    isPaused = false;
  }

  function stopLoop() {
    if (rafId) {
      cancelAnimationFrame(rafId);
      rafId = null;
    }
  }

  function handleResize() {
    if (!containerRef.value || !renderer) return;

    const w = containerRef.value.clientWidth;
    const h = containerRef.value.clientHeight;
    
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    
    renderer.setSize(w, h, false);
  }

  function setPixelRatio(ratio) {
    if (renderer) {
      renderer.setPixelRatio(ratio);
    }
  }

  function dispose() {
    stopLoop();
    
    if (renderer) {
      if (renderer.domElement && renderer.domElement.parentNode) {
        renderer.domElement.parentNode.removeChild(renderer.domElement);
      }
      renderer.dispose();
      renderer = null;
    }

    scene.traverse((obj) => {
      if (obj.geometry) obj.geometry.dispose();
      if (obj.material) {
        if (Array.isArray(obj.material)) {
          obj.material.forEach(m => m.dispose());
        } else {
          obj.material.dispose();
        }
      }
    });

    isInitialized.value = false;
  }

  onUnmounted(() => {
    dispose();
  });

  return {
    scene,
    camera,
    getRenderer: () => renderer,
    isInitialized,
    initRenderer,
    startLoop,
    pauseLoop,
    resumeLoop,
    stopLoop,
    handleResize,
    setPixelRatio,
    dispose,
  };
}