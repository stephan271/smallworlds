import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
const config = {
  kit: {
    adapter: adapter({
      pages: '../internal/webui/dist',
      assets: '../internal/webui/dist',
      strict: true
    }),
    csp: {
      mode: 'hash',
      directives: {
        'default-src': ['self'],
        'script-src': ['self'],
        'style-src': ['self', 'unsafe-inline'],
        'connect-src': ['self'],
        'img-src': ['self'],
        'font-src': ['self'],
        'base-uri': ['none'],
        'frame-ancestors': ['none'],
        'form-action': ['self']
      }
    }
  }
};

export default config;
