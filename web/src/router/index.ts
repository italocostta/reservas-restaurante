import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

// As rotas espelham as duas perguntas que o staff faz, e nada além:
//
//   "quem vem hoje, e onde?"  → /reservas   (a agenda — a tela do serviço)
//   "que mesas eu tenho?"     → /mesas      (o salão — se mexe uma vez por semestre)
//
// A raiz cai na AGENDA. É a tela que o staff abre cinquenta vezes por noite; o
// cadastro de mesa é manutenção.
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: '/reservas',
  },
  {
    path: '/reservas',
    name: 'reservas',
    component: () => import('@/views/AgendaView.vue'),
    meta: { titulo: 'Agenda' },
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
