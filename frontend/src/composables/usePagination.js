import { computed, ref, unref, watch } from "vue";

/**
 * 客户端分页：items 可为 ref / computed / 数组。
 * pageSize 默认 50，支持外部传入可选页容量。
 */
export function usePagination(items, options = {}) {
  const pageSizeOptions = options.pageSizeOptions || [20, 50, 100];
  const page = ref(1);
  const pageSize = ref(options.defaultPageSize || pageSizeOptions[1] || 50);

  const list = computed(() => {
    const value = unref(items);
    return Array.isArray(value) ? value : [];
  });

  const totalCount = computed(() => list.value.length);
  const totalPages = computed(() => Math.max(1, Math.ceil(totalCount.value / pageSize.value) || 1));

  const pagedItems = computed(() => {
    const size = pageSize.value;
    const start = (page.value - 1) * size;
    return list.value.slice(start, start + size);
  });

  const pageRangeLabel = computed(() => {
    if (totalCount.value === 0) return "0 / 0";
    const start = (page.value - 1) * pageSize.value + 1;
    const end = Math.min(page.value * pageSize.value, totalCount.value);
    return `${start}-${end} / ${totalCount.value}`;
  });

  // 页码窗口：首尾 + 当前邻域，中间用省略号
  const pageNumbers = computed(() => {
    const total = totalPages.value;
    const current = page.value;
    if (total <= 7) {
      return Array.from({ length: total }, (_, i) => i + 1);
    }
    const pages = new Set([1, total, current, current - 1, current + 1]);
    if (current <= 3) {
      pages.add(2);
      pages.add(3);
      pages.add(4);
    }
    if (current >= total - 2) {
      pages.add(total - 1);
      pages.add(total - 2);
      pages.add(total - 3);
    }
    return Array.from(pages)
      .filter((n) => n >= 1 && n <= total)
      .sort((a, b) => a - b);
  });

  watch(pageSize, () => {
    page.value = 1;
  });

  watch(totalPages, (next) => {
    if (page.value > next) page.value = next;
  });

  function goToPage(next) {
    const target = Number(next);
    if (!Number.isFinite(target)) return;
    page.value = Math.min(totalPages.value, Math.max(1, Math.trunc(target)));
  }

  function resetPage() {
    page.value = 1;
  }

  return {
    page,
    pageSize,
    pageSizeOptions,
    totalCount,
    totalPages,
    pagedItems,
    pageRangeLabel,
    pageNumbers,
    goToPage,
    resetPage,
  };
}