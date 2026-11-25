import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// components/ui and the libraries under it, except the few that bring a big
// dependency only one or two screens use (they load with those screens).
function uiPrimitive(id: string) {
  if (/[\\/]src[\\/]components[\\/]ui[\\/]/.test(id)) return !/[\\/](command|input-otp|chart|context-menu|radio-group)\.tsx$/.test(id)
  if (/node_modules[\\/]@radix-ui[\\/]react-(context-menu|radio-group)[\\/]/.test(id)) return false
  return /node_modules[\\/](@radix-ui|radix-ui|@floating-ui|react-remove-scroll|react-remove-scroll-bar|react-style-singleton|use-sidecar|use-callback-ref|aria-hidden|class-variance-authority|cn)[\\/]/.test(id)
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: {
    host: true,
    proxy: { '/api': process.env.COFFER_API ?? 'http://localhost:8080' },
  },
  build: {
    sourcemap: false,
    chunkSizeWarningLimit: 900,
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            // The framework every screen needs. It changes far less often than
            // the app, so browsers keep it across updates.
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|scheduler|react-router|@tanstack[\\/](query-core|react-query))[\\/]/,
              priority: 30,
            },
            // Icons, in one file rather than one request each.
            { name: 'icons', test: /node_modules[\\/]lucide-react[\\/]/, priority: 20 },
            // The UI primitives nearly every screen shares.
            { name: 'ui', test: uiPrimitive, priority: 10 },
          ],
        },
      },
    },
  },
})
