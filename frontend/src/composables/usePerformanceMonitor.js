import { ref, readonly } from 'vue';

export function usePerformanceMonitor() {
  const fps = ref(60);
  const qualityLevel = ref(2);  // 0=低, 1=中, 2=高
  
  let frameCount = 0;
  let lastTime = performance.now();
  let lowFrameStreak = 0;
  let callbacks = [];

  function measureFrame() {
    frameCount++;
    const now = performance.now();
    const delta = now - lastTime;
    
    if (delta >= 1000) {
      fps.value = Math.round((frameCount * 1000) / delta);
      frameCount = 0;
      lastTime = now;

      // 检测低帧率
      if (fps.value < 30) {
        lowFrameStreak++;
        if (lowFrameStreak >= 10 && qualityLevel.value > 0) {
          downgrade();
        }
      } else {
        lowFrameStreak = 0;
      }
    }
  }

  function downgrade() {
    const oldLevel = qualityLevel.value;
    qualityLevel.value = Math.max(0, qualityLevel.value - 1);
    
    if (oldLevel !== qualityLevel.value) {
      console.log(`[Performance] Auto downgrade to level ${qualityLevel.value}`);
      notifyCallbacks('downgrade', qualityLevel.value);
    }
  }

  function upgrade() {
    const oldLevel = qualityLevel.value;
    qualityLevel.value = Math.min(2, qualityLevel.value + 1);
    
    if (oldLevel !== qualityLevel.value) {
      console.log(`[Performance] Upgrade to level ${qualityLevel.value}`);
      notifyCallbacks('upgrade', qualityLevel.value);
    }
  }

  function setQualityLevel(level) {
    if (level >= 0 && level <= 2) {
      qualityLevel.value = level;
      lowFrameStreak = 0;
      notifyCallbacks('manual', level);
    }
  }

  function reset() {
    qualityLevel.value = 2;
    lowFrameStreak = 0;
    frameCount = 0;
    lastTime = performance.now();
  }

  function onQualityChange(callback) {
    callbacks.push(callback);
    return () => {
      callbacks = callbacks.filter(cb => cb !== callback);
    };
  }

  function notifyCallbacks(reason, level) {
    callbacks.forEach(cb => cb(level, reason));
  }

  return {
    fps: readonly(fps),
    qualityLevel: readonly(qualityLevel),
    measureFrame,
    downgrade,
    upgrade,
    setQualityLevel,
    reset,
    onQualityChange,
  };
}