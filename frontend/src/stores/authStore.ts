import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { authApi, TokenPair } from '@/api/client'

interface AuthState {
  accessToken:  string | null
  refreshToken: string | null
  userId:       string | null
  isAdmin:      boolean

  login:   (email: string, password: string) => Promise<void>
  refresh: () => Promise<boolean>
  logout:  () => void
  _setTokens: (pair: TokenPair) => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      accessToken:  null,
      refreshToken: null,
      userId:       null,
      isAdmin:      false,

      _setTokens(pair) {
        set({ accessToken: pair.access_token, refreshToken: pair.refresh_token })
      },

      async login(email, password) {
        const pair = await authApi.login(email, password)
        get()._setTokens(pair)
        const me = await authApi.me()
        set({ userId: me.user_id, isAdmin: me.is_admin })
      },

      async refresh() {
        const rt = get().refreshToken
        if (!rt) return false
        try {
          const pair = await authApi.refresh(rt)
          get()._setTokens(pair)
          return true
        } catch {
          set({ accessToken: null, refreshToken: null, userId: null, isAdmin: false })
          return false
        }
      },

      logout() {
        const rt = get().refreshToken
        if (rt) authApi.logout(rt).catch(() => {})
        set({ accessToken: null, refreshToken: null, userId: null, isAdmin: false })
      },
    }),
    {
      name: 'entkube-auth',
      // Only persist the refresh token; access tokens are short-lived.
      partialize: (s) => ({ refreshToken: s.refreshToken }),
    },
  ),
)
