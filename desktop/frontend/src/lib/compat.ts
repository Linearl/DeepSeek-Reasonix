// tl1181 探针（纯 performance.mark，零行为）：compat 是 main.tsx 的首个
// import，其模块体先于 main.tsx 体求值——这个 mark 即「应用模块图求值起点」，
// main.tsx 的 tl1181 段级计时以它为原点。
performance.mark("tl1181:compat-top");

type ObjectConstructorWithHasOwn = ObjectConstructor & {
  hasOwn?: (value: object, property: PropertyKey) => boolean;
};

// Safari 15.3 / the matching macOS WebKit used by older Wails shells predates
// Object.hasOwn. react-markdown 10 calls it while validating deprecated props,
// so install the tiny ES2022 primitive before React imports the markdown chunk.
export function installObjectHasOwnPolyfill(target: ObjectConstructorWithHasOwn = Object): void {
  if (typeof target.hasOwn === "function") return;
  Object.defineProperty(target, "hasOwn", {
    configurable: true,
    writable: true,
    value(value: object, property: PropertyKey) {
      return Object.prototype.hasOwnProperty.call(value, property);
    },
  });
}

installObjectHasOwnPolyfill();
