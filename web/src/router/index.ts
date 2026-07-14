import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

// As rotas espelham as duas perguntas que o staff faz, e nada além:
//
//   "que mesas eu tenho?"        → /mesas      (esta fatia)
//   "quem vem hoje, e onde?"     → /reservas   (próxima fatia)
//
// A raiz redireciona para /reservas e não para /mesas, mesmo com /mesas sendo o
// que está construído: a tela que o staff abre 50 vezes por noite é a agenda do
// dia. Cadastro de mesa se mexe uma vez por semestre.
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/mesas', // TEMPORÁRIO: vira /reservas quando a agenda existir.
  },
  {
    path: '/mesas',
    name: 'mesas',
    component: () => import('@/views/MesasView.vue'),
    meta: { titulo: 'Salão' },
  },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})
