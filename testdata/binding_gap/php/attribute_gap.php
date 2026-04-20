<?php
/** @intent 사용자 조회 API */
#[Route('/api/user')]
#[IsGranted('ROLE_USER')]
function getUser(): array {
    return [];
}
