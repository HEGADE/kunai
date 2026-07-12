/// <reference types="vite/client" />

// Fontsource variable packages ship CSS with no type declarations; we import
// them only for their side effect (registering @font-face). Declare them so tsc
// stops flagging the side-effect imports in main.ts.
declare module '@fontsource-variable/geist'
declare module '@fontsource-variable/geist-mono'
declare module '@fontsource-variable/source-serif-4'
