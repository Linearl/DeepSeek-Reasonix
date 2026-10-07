/// <reference types="vite/client" />

declare const __BUILD_COMMIT__: string;
declare const __BUILD_CHANNEL__: string;
/** Task 512: true only when REASONIX_CHIME_LOCAL_ASSETS=1 at build time —
 *  gates the Nintendo-owned Mario chime asset out of public builds. */
declare const __CHIME_LOCAL_ASSETS__: boolean;
