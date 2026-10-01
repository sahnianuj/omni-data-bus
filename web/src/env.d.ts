interface ImportMetaEnv {
  /** The release this SPA was built for (OC-058): set by `make build` and the Dockerfile. */
  readonly VITE_ODB_VERSION?: string
}
