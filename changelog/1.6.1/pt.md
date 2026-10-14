# SPK Ocular 1.6.1

- Os pacotes Linux DEB/RPM exigem glibc 2.35 ou posterior em vez de 2.39. Ubuntu 22.04 e Debian 12 podem instalá-los normalmente quando GTK 3 e WebKit2GTK 4.1 estão disponíveis.
- As duas arquiteturas Linux passam por verificações nativas de instalação, modo navegador e janela GTK no Ubuntu 22.04 antes da publicação. O empacotamento também rejeita executáveis que exigem símbolos glibc acima do mínimo declarado.
- O arquivo de verificação nativa acrescenta dois relatórios e capturas do Ubuntu 22.04 às evidências das seis plataformas existentes. São verificações de X11 e do espaço de usuário com o kernel do ambiente de teste; não certificam todos os kernels, drivers GPU ou sessões Wayland.
