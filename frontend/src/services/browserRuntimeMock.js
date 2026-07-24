// Browser-preview replacement for @wailsio/runtime.
// Keep this module side-effect free: importing it must never probe or call Wails.
const createAny = (source) => source;

const createArray = (element) => {
  if (element === createAny) {
    return (source) => (source === null ? [] : source);
  }
  return (source) => {
    if (source === null) {
      return [];
    }
    for (let index = 0; index < source.length; index += 1) {
      source[index] = element(source[index]);
    }
    return source;
  };
};

const createMap = (key, value) => {
  void key;
  if (value === createAny) {
    return (source) => (source === null ? {} : source);
  }
  return (source) => {
    if (source === null) {
      return {};
    }
    for (const sourceKey in source) {
      source[sourceKey] = value(source[sourceKey]);
    }
    return source;
  };
};

const createNullable = (element) => {
  if (element === createAny) {
    return createAny;
  }
  return (source) => (source === null ? null : element(source));
};

const createStruct = (fields) => {
  const allAny = Object.values(fields).every((field) => field === createAny);
  if (allAny) {
    return createAny;
  }
  return (source) => {
    for (const [name, createField] of Object.entries(fields)) {
      if (name in source) {
        source[name] = createField(source[name]);
      }
    }
    return source;
  };
};

// Generated Wails bindings import `Create` as a namespace and call its
// creation helpers while hydrating backend responses.
export const Create = {
  Any: createAny,
  ByteSlice: (source) => (source == null ? "" : source),
  Array: createArray,
  Map: createMap,
  Nullable: createNullable,
  Struct: createStruct,
  Events: {},
};
export class CancellablePromise extends Promise {}
const noop = () => {};
const resolved = () => Promise.resolve();

export const Events = {
  On: () => noop,
  Off: noop,
  Emit: noop,
};

export const Window = {
  Minimise: resolved,
  Close: resolved,
  Hide: resolved,
};

export const Browser = {
  OpenURL: (url) => {
    if (typeof window !== "undefined" && url) {
      window.open(url, "_blank", "noopener,noreferrer");
    }
    return Promise.resolve();
  },
};

export const Call = {
  ByName: () => Promise.resolve(undefined),
};