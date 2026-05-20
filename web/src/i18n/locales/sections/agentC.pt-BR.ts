/**
 * Fragmento PT-BR do agente C — sweep de i18n em Settings.
 *
 * Contém SÓ as chaves novas adicionadas pelos arquivos editados nessa rodada.
 * O merge final no pt-BR.ts é feito por outro processo — não duplicar aqui
 * o que já existe em pt-BR.ts (common.*, notify.*, errors.*, settings.*).
 */
export default {
    settings: {
        members: {
            title: 'Membros',
            sub: 'Pessoas que podem acessar esta instância do Prexel.',
            count: '{n} membro | {n} membros',
            countZero: 'Nenhum membro',
            new: 'Novo membro',
            edit: 'Editar membro',
            initialPassword: 'Senha inicial',
            status: {
                label: 'Status',
                active: 'Ativo',
                inactive: 'Inativo',
                blocked: 'Bloqueado',
            },
            roleNoneGlobal: 'Sem role global',
            teamsGlobalTag: 'Global',
            createdToast: 'Membro criado.',
            updatedToast: 'Membro atualizado.',
            removedToast: 'Membro removido.',
            remove: {
                title: 'Remover membro',
                body: 'Remover {email} da instância?',
                accept: 'Remover',
            },
            columns: {
                person: 'Pessoa',
                role: 'Role',
                teams: 'Times',
                status: 'Status',
            },
        },
        security: {
            twoFactor: {
                sub: 'Adicione uma camada extra de segurança à sua conta.',
                qrAlt: 'QR code TOTP',
                disableBody: 'Para desabilitar o 2FA, informe sua senha e o código atual do app autenticador.',
                passwordLabel: 'Senha',
                totpLabel: 'Código TOTP atual',
                regenerate: 'Regenerar',
                acknowledge: 'Entendi',
                passwordRequired: 'Senha obrigatória.',
                disableButton: 'Desabilitar 2FA',
                disableMissingFields: 'Senha e código TOTP atual obrigatórios.',
                disabledToast: '2FA desabilitado.',
            },
        },
    },
} as const
