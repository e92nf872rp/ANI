import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { MessagePlugin } from 'tdesign-react'
import 'tdesign-react/es/style/index.css'
import './styles.css'
import { routeTree } from './routeTree.gen'
import { hydrateSession } from './auth/session'
import { setAuthToken } from './api/auth'

const queryClient = new QueryClient()

const initialToken = hydrateSession()
if (initialToken) {
  setAuthToken(initialToken)
}

MessagePlugin.config({ placement: 'top', offset: [0, 16] })

const router = createRouter({
  routeTree,
  basepath: '/boss/',
  context: {
    queryClient,
  },
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
)
