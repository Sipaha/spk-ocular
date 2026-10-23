# SPK Ocular 1.10.0

- Conecta agentes locales mediante el servidor MCP integrado, con permisos explícitos y operaciones revisadas. Codex y Claude Code se configuran bajo petición; otros clientes disponen de configuración manual. MCP Streamable HTTP sustituye al protocolo local anterior; es necesario reconfigurar los clientes existentes.
- Consulta vistas nativas de Argo CD y Flux, estado de aplicaciones/controladores y enlaces a recursos. Las revisiones de edición y escalado muestran el contexto GitOps disponible.
- Investiga workloads, rutas de red Service/Ingress y releases de Helm mediante lecturas diagnósticas explícitas y limitadas, con fuentes y lagunas visibles.
- Añade columnas de etiquetas y guarda vistas de tablas. Consulta el estado y la renovación de Certificate de cert-manager sin leer contenido de Secret.
- Lee el historial de métricas Prometheus seleccionado explícitamente y recomendaciones VPA existentes; revisa configuración y fiabilidad de workloads con su contexto.
- Inspecciona informes existentes de configuración/RBAC de Trivy y políticas por namespace. Los informes de todo el clúster y la auditoría de una restricción Gatekeeper requieren activación independiente; la falta de evidencia sigue siendo desconocida.
- Importa informes KRR al almacenamiento temporal del backend o revisa un Job temporal de Kubernetes antes de ejecutar KRR. Consulta declaraciones de recursos por namespace de un Node seleccionado, sin confundirlas con capacidad libre medida.
- Importa localmente informes JSON de auditoría Polaris con límites, sin instalar un escáner ni consultar el clúster durante la importación.
- Combina logs de workloads seleccionados explícitamente, gestionando solapamientos, atribución de origen, filtrado y exportación finita.
- Abre auxiliares Node Shell y Files tras revisión, con ciclo de vida compartido y registro persistente de limpieza. Files permite borrar y seleccionar archivos mostrados; Alt+Shift+Enter separa o vuelve a acoplar Files/Shell.
- Aprende Kubernetes con ayuda sin conexión y una introducción aislada basada en tareas, comentarios diagnósticos más claros y ocho idiomas.
- La selección de Pods explica la revisión del rollout y su disponibilidad; las indicaciones de comparación son más claras. Los reenvíos conservan el estado de reconexión al retirar upstreams antiguos.
- Los builds utilizan Go 1.26.10, que incluye la corrección de seguridad de confianza en certificados.
