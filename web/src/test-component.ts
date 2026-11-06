import { createRenderer, h, ssrContextKey, type Component, type ComponentOptions } from 'vue'

// Exercise real setup/watch lifecycles without a browser or rendering unrelated child controls.
const renderer = createRenderer<object, object>({
  createElement: () => ({}),
  createText: () => ({}),
  createComment: () => ({}),
  insert: () => {},
  remove: () => {},
  parentNode: () => null,
  nextSibling: () => null,
  setText: () => {},
  setElementText: () => {},
  patchProp: () => {},
})

export function mountSetup<T>(component: Component, props: Record<string, unknown>) {
  let state: T | undefined
  const harness = {
    ...(component as ComponentOptions),
    render: () => null,
    created() {
      state = (this as unknown as { $: { setupState: T } }).$.setupState
    },
  }
  const app = renderer.createApp({ render: () => h(harness, { ...props }) })
  app.provide(ssrContextKey, { modules: new Set<string>() })
  app.mount({})
  if (state === undefined) throw new Error('Component setup was not captured')
  return { state, unmount: () => app.unmount() }
}
