import { afterEach, beforeEach, vi } from 'vitest'

/** A controllable EventSource: tests emit events on the latest instance. */
export class FakeEventSource {
  static instances: FakeEventSource[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSED = 2
  readyState = 0
  onopen: ((e: Event) => void) | null = null
  onerror: ((e: Event) => void) | null = null
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>()
  constructor(
    public url: string,
    public init?: EventSourceInit,
  ) {
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  removeEventListener() {}
  close() {
    this.readyState = 2
  }
  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }
  fail(closed = false) {
    this.readyState = closed ? 2 : 0
    this.onerror?.(new Event('error'))
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify(data) }))
  }
  static latest() {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1]!
  }
}

/** Installs FakeEventSource as the global EventSource for the calling file. */
export function withFakeEventSource() {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => vi.unstubAllGlobals())
}
