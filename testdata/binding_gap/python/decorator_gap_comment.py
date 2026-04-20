# @intent 사용자 조회 엔드포인트
# @domainRule 인증 필요
@app.route('/api/user')
@login_required
def get_user():
    pass
