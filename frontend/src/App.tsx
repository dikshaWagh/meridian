import { Suspense, lazy } from 'react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { AppShell } from './components/layout/AppShell'
import { LoadingBlock } from './components/ui/States'
import { ChamferFrame } from './components/ui/hud'
import { ModeProvider } from './lib/mode'

const Landing = lazy(() => import('./pages/Landing'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const Playground = lazy(() => import('./pages/Playground'))
const MLPreview = lazy(() => import('./pages/MLPreview'))
const NotFound = lazy(() => import('./pages/NotFound'))

export default function App() {
  return (
    <ModeProvider>
      <BrowserRouter>
        <AppShell>
          <Suspense
            fallback={
              <div className="panel p-5">
                <ChamferFrame />
                <LoadingBlock label="Loading page" />
              </div>
            }
          >
            <Routes>
              <Route path="/" element={<Landing />} />
              <Route path="/dashboard" element={<Dashboard />} />
              <Route path="/playground" element={<Playground />} />
              <Route path="/ml" element={<MLPreview />} />
              <Route path="*" element={<NotFound />} />
            </Routes>
          </Suspense>
        </AppShell>
      </BrowserRouter>
    </ModeProvider>
  )
}
